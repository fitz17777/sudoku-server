package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/user/sudoku/internal/game"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
)

// LeaderboardHandler handles leaderboard display.
type LeaderboardHandler struct {
	store *redisstore.Client
	tmpl  *templates.Renderer
}

func NewLeaderboardHandler(store *redisstore.Client, tmpl *templates.Renderer) *LeaderboardHandler {
	return &LeaderboardHandler{store: store, tmpl: tmpl}
}

// ShowDifficulty renders the cumulative leaderboard for an entire difficulty.
func (h *LeaderboardHandler) ShowDifficulty(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	diff := game.Difficulty(chi.URLParam(r, "difficulty"))

	valid := false
	for _, d := range game.Difficulties {
		if d == diff {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, "invalid difficulty", http.StatusBadRequest)
		return
	}

	topScores, _ := h.store.GetDifficultyLeaderboard(r.Context(), diff, 20)

	renderPage(w, h.tmpl, "pages/leaderboard_difficulty.html", map[string]interface{}{
		"User":         user,
		"Difficulty":   diff,
		"Difficulties": game.Difficulties,
		"TopScores":    topScores,
	})
}

// ShowMiniGame renders the wins leaderboard for a mini-game (tictactoe/connectfour/checkers).
func (h *LeaderboardHandler) ShowMiniGame(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	gameType := chi.URLParam(r, "gametype")
	switch gameType {
	case game.GameTypeTicTacToe, game.GameTypeConnectFour, game.GameTypeCheckers:
	default:
		http.Error(w, "unknown game type", http.StatusBadRequest)
		return
	}

	entries, _ := h.store.GetWinLeaderboard(r.Context(), gameType, 20)

	renderPage(w, h.tmpl, "pages/leaderboard_minigame.html", map[string]interface{}{
		"User":     user,
		"GameType": gameType,
		"Entries":  entries,
	})
}

// Show renders the leaderboard for a specific puzzle.
func (h *LeaderboardHandler) Show(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	diff := game.Difficulty(chi.URLParam(r, "difficulty"))
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number < 1 || number > game.PuzzlesPerDifficulty {
		http.Error(w, "invalid puzzle", http.StatusBadRequest)
		return
	}

	entries, err := h.store.GetLeaderboard(r.Context(), diff, number, 20)
	if err != nil {
		http.Error(w, "leaderboard unavailable", http.StatusInternalServerError)
		return
	}

	playerRank, _ := h.store.GetPlayerRank(r.Context(), diff, number, user.UserID)
	personalBest, _ := h.store.GetScore(r.Context(), user.UserID, diff, number)

	renderPage(w, h.tmpl, "pages/leaderboard.html", map[string]interface{}{
		"User":         user,
		"Difficulty":   diff,
		"Number":       number,
		"Entries":      entries,
		"PlayerRank":   playerRank,
		"PersonalBest": personalBest,
	})
}
