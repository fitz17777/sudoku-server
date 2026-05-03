package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/user/sudoku/internal/auth"
	"github.com/user/sudoku/internal/config"
	"github.com/user/sudoku/internal/game"
	"github.com/user/sudoku/internal/handler"
	"github.com/user/sudoku/internal/hub"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
	"github.com/user/sudoku/web"
)

var ready atomic.Bool // true once puzzle seeding is complete

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	// Connect to Redis
	store, err := redisstore.New(cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		log.Fatalf("redis connection failed: %v", err)
	}
	defer store.Close()
	log.Println("redis: connected")

	// Parse templates from embedded FS (web package embeds web/templates/).
	tmplFS, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		log.Fatalf("template FS error: %v", err)
	}
	tmpl, err := templates.New(tmplFS)
	if err != nil {
		log.Fatalf("template parse error: %v", err)
	}
	log.Println("templates: parsed")

	// Initialise OIDC provider
	oidcProvider, err := auth.NewProvider(
		context.Background(),
		cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, cfg.OIDCRedirectURL,
	)
	if err != nil {
		log.Fatalf("OIDC provider error: %v", err)
	}
	log.Println("oidc: provider initialised")

	// Start puzzle seeder in background (before accepting traffic)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := game.Seed(ctx, store, cfg.PuzzleRegenerate); err != nil {
			log.Printf("seeder error: %v", err)
		}
		ready.Store(true)
		log.Println("seeder: 125 puzzles ready")
	}()

	// Build WebSocket hub
	wsHub := hub.New(store, tmpl)
	go wsHub.Run(ctx)

	// Build handlers
	authH := handler.NewAuthHandler(oidcProvider, store, tmpl, cfg.BaseURL)
	gameH := handler.NewGameHandler(store, tmpl)
	multiH := handler.NewMultiHandler(store, tmpl, wsHub)
	lbH := handler.NewLeaderboardHandler(store, tmpl)
	wsH := handler.NewWSHandler(wsHub)
	authMW := auth.Middleware(store)

	// Build router
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	// Static assets
	staticFS, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("static FS error: %v", err)
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Public routes
	r.Get("/healthz", healthz)
	r.Get("/login", authH.Login)
	r.Get("/callback", authH.Callback)

	// Home — redirect to /play/solo (auth middleware will redirect to /login if needed)
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/play/solo", http.StatusFound)
	})

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authMW)

		r.Get("/logout", authH.Logout)

		// Solo
		r.Get("/play/solo", gameH.SelectPuzzle)
		r.Get("/play/solo/{difficulty}", gameH.RandomGame)
		r.Get("/play/solo/{difficulty}/{number}", gameH.StartGame)
		r.Post("/play/solo/{gameID}/cell", gameH.CellFill)

		// Multiplayer
		r.Get("/play/multi/create", multiH.CreateRoomPage)
		r.Post("/play/multi/create", multiH.CreateRoom)
		r.Get("/play/multi/join", multiH.JoinRoomPage)
		r.Post("/play/multi/join", multiH.JoinRoom)
		r.Get("/play/multi/room/{code}", multiH.RoomPage)

		// Leaderboard
		r.Get("/leaderboard", func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "/leaderboard/easy", http.StatusFound)
		})
		r.Get("/leaderboard/{difficulty}", lbH.ShowDifficulty)
		r.Get("/leaderboard/{difficulty}/{number}", lbH.Show)

		// WebSocket
		r.Get("/ws", wsH.ServeHTTP)
	})

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("server: listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("server: shutting down…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
}

func healthz(w http.ResponseWriter, r *http.Request) {
	if !ready.Load() {
		http.Error(w, "seeding in progress", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
