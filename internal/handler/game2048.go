package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user/sudoku/internal/game"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
)

// Game2048Handler handles solo 2048 routes.
type Game2048Handler struct {
	store *redisstore.Client
	tmpl  *templates.Renderer
}

func NewGame2048Handler(store *redisstore.Client, tmpl *templates.Renderer) *Game2048Handler {
	return &Game2048Handler{store: store, tmpl: tmpl}
}

// Landing shows the 2048 start page with leaderboard and a "New Game" button.
func (h *Game2048Handler) Landing(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	lb, _ := h.store.Get2048Leaderboard(r.Context(), 10)
	best, _ := h.store.Get2048PersonalBest(r.Context(), user.UserID)
	renderPage(w, h.tmpl, "pages/game2048_solo.html", map[string]interface{}{
		"User":        user,
		"Leaderboard": lb,
		"BestScore":   best,
		"Game":        nil,
	})
}

// StartGame creates a new game and redirects to the game page.
func (h *Game2048Handler) StartGame(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	best, _ := h.store.Get2048PersonalBest(r.Context(), user.UserID)
	gs := game.Game2048State{
		GameID:    generateID(),
		UserID:    user.UserID,
		Board:     game.NewBoard2048(),
		Score:     0,
		BestScore: best,
		Status:    game.StatusPlaying,
		StartedAt: time.Now(),
	}
	if err := h.store.Store2048State(r.Context(), gs); err != nil {
		http.Error(w, "could not create game", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/play/solo/2048/"+gs.GameID, http.StatusFound)
}

// GamePage renders an in-progress game board.
func (h *Game2048Handler) GamePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	gameID := chi.URLParam(r, "gameID")
	gs, err := h.store.Get2048State(r.Context(), gameID)
	if err != nil || gs.UserID != user.UserID {
		http.Redirect(w, r, "/play/solo/2048", http.StatusFound)
		return
	}
	lb, _ := h.store.Get2048Leaderboard(r.Context(), 10)
	renderPage(w, h.tmpl, "pages/game2048_solo.html", map[string]interface{}{
		"User":        user,
		"Game":        gs,
		"Leaderboard": lb,
		"BestScore":   gs.BestScore,
	})
}

// Move processes one directional move and returns OOB HTML swaps.
func (h *Game2048Handler) Move(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	gameID := chi.URLParam(r, "gameID")
	dir := r.FormValue("direction")

	gs, err := h.store.Get2048State(r.Context(), gameID)
	if err != nil || gs.UserID != user.UserID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if gs.Status != game.StatusPlaying {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	newBoard, points, changed := game.Move2048(gs.Board, dir)
	if !changed {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	gs.Board = game.SpawnTile(newBoard)
	gs.Score += points
	if gs.Score > gs.BestScore {
		gs.BestScore = gs.Score
	}

	if game.Is2048Won(gs.Board) {
		gs.Status = game.StatusComplete
		h.store.Record2048HighScore(r.Context(), user.UserID, user.DisplayName, gs.Score)
	} else if game.Is2048Over(gs.Board) {
		gs.Status = game.StatusFailed
		h.store.Record2048HighScore(r.Context(), user.UserID, user.DisplayName, gs.Score)
	}

	if err := h.store.Store2048State(r.Context(), *gs); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	renderPartialInline(w, h.tmpl, "partials/game2048_board.html", gs)
}
