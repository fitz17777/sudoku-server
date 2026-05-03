package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user/sudoku/internal/auth"
	"github.com/user/sudoku/internal/game"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
)

// GameHandler handles solo game routes.
type GameHandler struct {
	store *redisstore.Client
	tmpl  *templates.Renderer
}

func NewGameHandler(store *redisstore.Client, tmpl *templates.Renderer) *GameHandler {
	return &GameHandler{store: store, tmpl: tmpl}
}

// SelectGame shows the tile-style game selection page for solo play.
func (h *GameHandler) SelectGame(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	renderPage(w, h.tmpl, "pages/game_select.html", map[string]interface{}{
		"User": user,
	})
}

// SelectPuzzle shows the Sudoku difficulty selection page.
func (h *GameHandler) SelectPuzzle(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	renderPage(w, h.tmpl, "pages/singleplayer.html", map[string]interface{}{
		"User":         user,
		"Difficulties": game.Difficulties,
	})
}

// RandomGame picks a random puzzle within a difficulty and redirects to StartGame.
func (h *GameHandler) RandomGame(w http.ResponseWriter, r *http.Request) {
	diff := game.Difficulty(chi.URLParam(r, "difficulty"))
	n := mathrand.Intn(game.PuzzlesPerDifficulty) + 1
	http.Redirect(w, r, fmt.Sprintf("/play/solo/%s/%d", diff, n), http.StatusFound)
}

// StartGame loads a specific puzzle and begins a solo game.
func (h *GameHandler) StartGame(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	diff := game.Difficulty(chi.URLParam(r, "difficulty"))
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number < 1 || number > game.PuzzlesPerDifficulty {
		http.Error(w, "invalid puzzle", http.StatusBadRequest)
		return
	}

	puzzle, err := h.store.GetPuzzle(r.Context(), diff, number)
	if err != nil {
		http.Error(w, "puzzle not found", http.StatusNotFound)
		return
	}

	// Create a solo game state
	gameID := generateID()
	gs := game.GameState{
		GameID:    gameID,
		PuzzleRef: game.PuzzleRef{Difficulty: diff, Number: number},
		BoardState: puzzle.Grid,
		Mode:      game.ModeSolo,
		Players: []game.Player{
			{
				UserID:      user.UserID,
				Username:    user.Username,
				DisplayName: user.DisplayName,
				Color:       game.PlayerColors[0],
			},
		},
		CellOwners: make(map[string]string),
		StartedAt:  time.Now(),
		Status:     game.StatusPlaying,
	}
	if err := h.store.StoreGame(r.Context(), gs); err != nil {
		http.Error(w, "could not create game", http.StatusInternalServerError)
		return
	}

	h.store.IncrPuzzleAttempt(r.Context(), diff, number)

	// Retrieve personal best
	personalBest, _ := h.store.GetScore(r.Context(), user.UserID, diff, number)

	renderPage(w, h.tmpl, "pages/game.html", map[string]interface{}{
		"User":         user,
		"Game":         gs,
		"Puzzle":       puzzle,
		"PersonalBest": personalBest,
	})
}

// CellFill handles a single cell entry via HTMX POST.
func (h *GameHandler) CellFill(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	gameID := chi.URLParam(r, "gameID")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	row, _ := strconv.Atoi(r.FormValue("row"))
	col, _ := strconv.Atoi(r.FormValue("col"))
	val, _ := strconv.Atoi(r.FormValue("value"))

	gs, err := h.store.GetGame(r.Context(), gameID)
	if err != nil || gs.Status != game.StatusPlaying {
		http.Error(w, "game not found", http.StatusNotFound)
		return
	}
	if gs.Players[0].UserID != user.UserID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	puzzle, err := h.store.GetPuzzle(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number)
	if err != nil {
		http.Error(w, "puzzle not found", http.StatusNotFound)
		return
	}

	// Don't allow overwriting original clue cells
	if puzzle.Grid[row][col] != 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	correct := puzzle.Solution[row][col] == val
	if val == 0 {
		gs.BoardState[row][col] = 0
	} else {
		gs.BoardState[row][col] = val
		if !correct {
			gs.Players[0].Mistakes++
		} else {
			// Award per-cell score based on elapsed time and difficulty
			gs.Players[0].Score += game.ScoreForCell(gs.PuzzleRef.Difficulty, time.Since(gs.StartedAt).Seconds())
		}
	}

	// 3-mistake failure
	if gs.Players[0].Mistakes >= 3 {
		gs.Status = game.StatusFailed
		h.store.StoreGame(r.Context(), *gs)
		w.Header().Set("Content-Type", "text/html")
		renderPartialInline(w, h.tmpl, "partials/cell.html", map[string]interface{}{
			"Row": row, "Col": col, "Value": val,
			"Correct": correct, "Owner": user.UserID,
			"Game": gs, "Mode": string(game.ModeSolo), "OOB": true,
		})
		renderPartialInline(w, h.tmpl, "partials/score_panel.html", map[string]interface{}{
			"Game": gs, "OOB": true, "Solo": true,
		})
		renderPartialInline(w, h.tmpl, "partials/game_failed.html", map[string]interface{}{
			"Game": gs, "OOB": true,
		})
		return
	}

	if err := h.store.StoreGame(r.Context(), *gs); err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	// Check completion
	completed := game.IsBoardComplete(gs.BoardState, puzzle.Solution)
	var speedPct int
	var stats game.PuzzleStats

	if completed {
		elapsed := time.Since(gs.StartedAt).Seconds()
		score := gs.Players[0].Score // accumulated per-cell throughout the game
		t := time.Now()
		gs.Players[0].FinishedAt = &t
		gs.Players[0].Finished = true
		gs.Status = game.StatusComplete

		h.store.UpsertScore(r.Context(), user.UserID, gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number, score, int(elapsed))
		h.store.UpdateLeaderboard(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number, user.UserID, user.DisplayName, score, int(elapsed))
		h.store.UpdateDifficultyLeaderboard(r.Context(), gs.PuzzleRef.Difficulty, user.UserID, user.DisplayName, score, int(elapsed))
		h.store.IncrPuzzleCompletion(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number)
		h.store.RecordCompletionTime(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number, gs.GameID, int(elapsed))
		h.store.StoreGame(r.Context(), *gs)

		stats, _ = h.store.GetPuzzleStats(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number)
		speedPct, _ = h.store.GetSpeedPercentile(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number, gs.GameID)
	}

	// Return OOB swaps for the cell + score panel (and optionally game-complete)
	w.Header().Set("Content-Type", "text/html")

	renderPartialInline(w, h.tmpl, "partials/cell.html", map[string]interface{}{
		"Row": row, "Col": col, "Value": val,
		"Correct": correct, "Owner": user.UserID,
		"Game": gs, "Mode": string(game.ModeSolo), "OOB": true,
	})
	renderPartialInline(w, h.tmpl, "partials/score_panel.html", map[string]interface{}{
		"Game": gs, "OOB": true, "Solo": true,
	})

	if completed {
		leaderboard, _ := h.store.GetLeaderboard(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number, 10)
		playerRank, _ := h.store.GetPlayerRank(r.Context(), gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number, user.UserID)
		renderPartialInline(w, h.tmpl, "partials/leaderboard_result.html", map[string]interface{}{
			"Game": gs, "Leaderboard": leaderboard,
			"PlayerRank":      playerRank,
			"SuccessRate":     stats.SuccessRate(),
			"TotalAttempts":   stats.Attempts,
			"SpeedPercentile": speedPct,
			"OOB":             true,
		})
	}
}

// --- helpers ---

func mustUser(r *http.Request) *game.User {
	u := auth.GetUser(r.Context())
	if u == nil {
		return &game.User{}
	}
	user, _ := u.(*game.User)
	return user
}

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func renderPage(w http.ResponseWriter, tmpl *templates.Renderer, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Render(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func renderPartialInline(w http.ResponseWriter, tmpl *templates.Renderer, name string, data interface{}) {
	_ = tmpl.RenderPartial(w, name, data)
}

