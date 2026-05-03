package hub

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	mathrand "math/rand"
	"strings"
	"time"

	"github.com/user/sudoku/internal/game"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
)

// Room manages the state and clients for a multiplayer game room.
type Room struct {
	Code    string
	clients map[string]*Client // user_id → client
	store   *redisstore.Client
	tmpl    *templates.Renderer
}

func newRoom(code string, store *redisstore.Client, tmpl *templates.Renderer) *Room {
	return &Room{
		Code:    code,
		clients: make(map[string]*Client),
		store:   store,
		tmpl:    tmpl,
	}
}

func (r *Room) addClient(c *Client) {
	r.clients[c.user.UserID] = c
}

func (r *Room) removeClient(c *Client) {
	delete(r.clients, c.user.UserID)
}

func (r *Room) isEmpty() bool {
	return len(r.clients) == 0
}

// broadcast sends a message to all clients in the room.
func (r *Room) broadcast(data []byte) {
	for _, c := range r.clients {
		select {
		case c.send <- data:
		default:
			close(c.send)
		}
	}
}

// sendTo sends a message to a specific user in the room.
func (r *Room) sendTo(userID string, data []byte) {
	if c, ok := r.clients[userID]; ok {
		select {
		case c.send <- data:
		default:
			close(c.send)
		}
	}
}

// dispatch processes an inbound message from a client.
func (r *Room) dispatch(ctx context.Context, msg InboundMsg) {
	switch msg.Type {
	case "join_room":
		r.handleJoin(ctx, msg)
	case "cell_fill", "cell_clear":
		r.handleCellFill(ctx, msg)
	case "start_game":
		r.handleStartGame(ctx, msg)
	default:
		log.Printf("room %s: unknown message type %q from %s", r.Code, msg.Type, msg.UserID)
	}
}

func (r *Room) handleJoin(ctx context.Context, msg InboundMsg) {
	room, err := r.store.GetRoom(ctx, r.Code)
	if err != nil {
		log.Printf("room %s: handleJoin error: %v", r.Code, err)
		return
	}

	alreadyIn := false
	for _, pid := range room.Players {
		if pid == msg.UserID {
			alreadyIn = true
			break
		}
	}
	if !alreadyIn && len(room.Players) < 2 {
		room.Players = append(room.Players, msg.UserID)
		if err := r.store.StoreRoom(ctx, *room); err != nil {
			log.Printf("room %s: StoreRoom error: %v", r.Code, err)
		}
	}

	html := r.renderPlayerStatus(room)
	r.broadcast(buildWSMsg("player_joined", html))
}

func (r *Room) handleStartGame(ctx context.Context, msg InboundMsg) {
	room, err := r.store.GetRoom(ctx, r.Code)
	if err != nil {
		log.Printf("room %s: handleStartGame room error: %v", r.Code, err)
		return
	}
	if room.Status != game.StatusWaiting {
		return
	}

	diff := game.Difficulty(msg.Difficulty)
	room.Difficulty = diff
	room.Mode = game.GameMode(msg.Mode)
	room.Status = game.StatusPlaying

	// Pick random puzzle(s). Side-by-side gives each player a different puzzle;
	// shared-board modes (collaborative, competitive) use one puzzle for both.
	n1 := randomPuzzleNumber()

	switch room.Mode {
	case game.ModeSideBySide:
		n2 := randomPuzzleNumberExcluding(n1)
		nums := []int{n1, n2}
		for i, uid := range room.Players {
			c := r.clients[uid]
			if c == nil {
				continue
			}
			puzNum := nums[i%len(nums)]
			puzzle, err := r.store.GetPuzzle(ctx, diff, puzNum)
			if err != nil {
				log.Printf("room %s: GetPuzzle error: %v", r.Code, err)
				return
			}
			gs := buildGameState(uid, c.user.Username, c.user.DisplayName, i, *puzzle, room.Mode)
			room.GameIDs = append(room.GameIDs, gs.GameID)
			if err := r.store.StoreGame(ctx, gs); err != nil {
				log.Printf("room %s: StoreGame error: %v", r.Code, err)
			}
			r.store.IncrPuzzleAttempt(ctx, diff, puzNum)
		}
	default:
		puzzle, err := r.store.GetPuzzle(ctx, diff, n1)
		if err != nil {
			log.Printf("room %s: GetPuzzle error: %v", r.Code, err)
			return
		}
		gs := buildSharedGameState(room.Players, r.clients, *puzzle, room.Mode)
		room.GameIDs = []string{gs.GameID}
		if err := r.store.StoreGame(ctx, gs); err != nil {
			log.Printf("room %s: StoreGame error: %v", r.Code, err)
		}
		room.Number = n1
		r.store.IncrPuzzleAttempt(ctx, diff, n1)
	}

	if err := r.store.StoreRoom(ctx, *room); err != nil {
		log.Printf("room %s: StoreRoom error: %v", r.Code, err)
	}

	for i, uid := range room.Players {
		c := r.clients[uid]
		if c == nil {
			continue
		}
		var gameID string
		if room.Mode == game.ModeSideBySide && i < len(room.GameIDs) {
			gameID = room.GameIDs[i]
		} else if len(room.GameIDs) > 0 {
			gameID = room.GameIDs[0]
		}

		gs, err := r.store.GetGame(ctx, gameID)
		if err != nil {
			continue
		}

		var opponentGS *game.GameState
		if room.Mode == game.ModeSideBySide {
			oppIdx := 1 - i
			if oppIdx >= 0 && oppIdx < len(room.GameIDs) {
				opponentGS, _ = r.store.GetGame(ctx, room.GameIDs[oppIdx])
			}
		}

		html := r.renderGameStarted(gs, opponentGS, room)
		c.send <- buildWSMsg("game_started", html)
	}
}

func (r *Room) handleCellFill(ctx context.Context, msg InboundMsg) {
	room, err := r.store.GetRoom(ctx, r.Code)
	if err != nil {
		return
	}
	if room.Status != game.StatusPlaying {
		return
	}

	switch room.Mode {
	case game.ModeSideBySide:
		r.handleSideBySideCellFill(ctx, msg, room)
	default:
		r.handleSharedCellFill(ctx, msg, room)
	}
}

func (r *Room) handleSharedCellFill(ctx context.Context, msg InboundMsg, room *game.Room) {
	if len(room.GameIDs) == 0 {
		return
	}
	gs, err := r.store.GetGame(ctx, room.GameIDs[0])
	if err != nil {
		return
	}
	puzzle, err := r.store.GetPuzzle(ctx, gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number)
	if err != nil {
		return
	}
	if puzzle.Grid[msg.Row][msg.Col] != 0 {
		return // don't overwrite original clues
	}

	correct := puzzle.Solution[msg.Row][msg.Col] == msg.Value
	cellKey := fmt.Sprintf("%d,%d", msg.Row, msg.Col)

	if room.Mode == game.ModeCompetitive {
		if _, owned := gs.CellOwners[cellKey]; owned {
			return
		}
	}

	if msg.Type == "cell_clear" || msg.Value == 0 {
		gs.BoardState[msg.Row][msg.Col] = 0
		delete(gs.CellOwners, cellKey)
	} else {
		gs.BoardState[msg.Row][msg.Col] = msg.Value
		if room.Mode == game.ModeCollaborative {
			// Shared score and mistakes: apply to all players equally
			if !correct {
				for i := range gs.Players {
					gs.Players[i].Mistakes++
				}
			} else {
				pts := game.ScoreForCell(gs.PuzzleRef.Difficulty, time.Since(gs.StartedAt).Seconds())
				for i := range gs.Players {
					gs.Players[i].Score += pts
				}
			}
		} else {
			// Competitive: per-player tracking
			for i := range gs.Players {
				if gs.Players[i].UserID == msg.UserID {
					if !correct {
						gs.Players[i].Mistakes++
					} else {
						gs.Players[i].Score += game.ScoreForCell(gs.PuzzleRef.Difficulty, time.Since(gs.StartedAt).Seconds())
						// Store player color (not userID) so CSS classes work directly
						gs.CellOwners[cellKey] = gs.Players[i].Color
					}
					break
				}
			}
		}
	}

	if err := r.store.StoreGame(ctx, *gs); err != nil {
		return
	}

	cellHTML := r.renderCell(msg.Row, msg.Col, msg.Value, correct, msg.UserID, gs, room.Mode)
	r.broadcast(buildWSMsg("board_update", cellHTML))

	scoreHTML := r.renderScorePanel(gs)
	r.broadcast(buildWSMsg("score_update", scoreHTML))

	if game.IsBoardComplete(gs.BoardState, puzzle.Solution) {
		r.finalizeSharedGame(ctx, gs, room)
	}
}

func (r *Room) handleSideBySideCellFill(ctx context.Context, msg InboundMsg, room *game.Room) {
	var myIdx int = -1
	for i, uid := range room.Players {
		if uid == msg.UserID {
			myIdx = i
			break
		}
	}
	if myIdx < 0 || myIdx >= len(room.GameIDs) {
		return
	}

	gs, err := r.store.GetGame(ctx, room.GameIDs[myIdx])
	if err != nil {
		return
	}
	puzzle, err := r.store.GetPuzzle(ctx, gs.PuzzleRef.Difficulty, gs.PuzzleRef.Number)
	if err != nil {
		return
	}
	if puzzle.Grid[msg.Row][msg.Col] != 0 {
		return
	}

	correct := puzzle.Solution[msg.Row][msg.Col] == msg.Value

	if msg.Type == "cell_clear" || msg.Value == 0 {
		gs.BoardState[msg.Row][msg.Col] = 0
	} else {
		gs.BoardState[msg.Row][msg.Col] = msg.Value
		for i := range gs.Players {
			if gs.Players[i].UserID == msg.UserID {
				if !correct {
					gs.Players[i].Mistakes++
				} else {
					gs.Players[i].Score += game.ScoreForCell(gs.PuzzleRef.Difficulty, time.Since(gs.StartedAt).Seconds())
				}
				break
			}
		}
	}

	if err := r.store.StoreGame(ctx, *gs); err != nil {
		return
	}

	cellHTML := r.renderCell(msg.Row, msg.Col, msg.Value, correct, msg.UserID, gs, game.ModeSideBySide)
	r.sendTo(msg.UserID, buildWSMsg("board_update", cellHTML))

	oppIdx := 1 - myIdx
	if oppIdx >= 0 && oppIdx < len(room.Players) {
		oppUID := room.Players[oppIdx]
		// Bundle cell + acting player's updated score so opponent sees live score/hearts
		oppCellHTML := r.renderOpponentCell(msg.Row, msg.Col, msg.Value)
		oppScoreHTML := r.renderOppScorePanel(gs)
		r.sendTo(oppUID, buildWSMsg("opponent_board", oppCellHTML+oppScoreHTML))
	}

	scoreHTML := r.renderScorePanel(gs)
	r.sendTo(msg.UserID, buildWSMsg("score_update", scoreHTML))

	if game.IsBoardComplete(gs.BoardState, puzzle.Solution) {
		r.finalizeSideBySideGame(ctx, gs, room, puzzle, msg.UserID)
	}
}

func (r *Room) finalizeSharedGame(ctx context.Context, gs *game.GameState, room *game.Room) {
	now := time.Now()
	elapsed := now.Sub(gs.StartedAt).Seconds()
	diff := gs.PuzzleRef.Difficulty
	number := gs.PuzzleRef.Number

	if room.Mode == game.ModeCollaborative {
		// All players already carry the same shared score (synced during play).
		sharedScore := gs.Players[0].Score
		names := make([]string, 0, len(gs.Players))
		for i := range gs.Players {
			p := &gs.Players[i]
			t := now
			p.FinishedAt = &t
			p.Finished = true
			p.Score = sharedScore
			names = append(names, p.DisplayName)
			r.store.UpsertScore(ctx, p.UserID, diff, number, sharedScore, int(elapsed))
		}
		collabKey := "collab_" + gs.GameID
		r.store.UpdateLeaderboard(ctx, diff, number, collabKey, strings.Join(names, " & "), sharedScore, int(elapsed))
	} else {
		// Competitive: use each player's per-cell accumulated score.
		for i := range gs.Players {
			p := &gs.Players[i]
			t := now
			p.FinishedAt = &t
			p.Finished = true
			r.store.UpsertScore(ctx, p.UserID, diff, number, p.Score, int(elapsed))
			r.store.UpdateLeaderboard(ctx, diff, number, p.UserID, p.DisplayName, p.Score, int(elapsed))
		}
	}

	// Update difficulty-level cumulative leaderboard for each individual player.
	for _, p := range gs.Players {
		r.store.UpdateDifficultyLeaderboard(ctx, diff, p.UserID, p.DisplayName, p.Score, int(elapsed))
	}

	gs.Status = game.StatusComplete
	r.store.StoreGame(ctx, *gs)
	room.Status = game.StatusComplete
	r.store.StoreRoom(ctx, *room)

	r.store.IncrPuzzleCompletion(ctx, diff, number)
	r.store.RecordCompletionTime(ctx, diff, number, gs.GameID, int(elapsed))
	stats, _ := r.store.GetPuzzleStats(ctx, diff, number)
	speedPct, _ := r.store.GetSpeedPercentile(ctx, diff, number, gs.GameID)

	leaderboard, _ := r.store.GetLeaderboard(ctx, diff, number, 10)
	html := r.renderGameComplete(gs, leaderboard, stats, speedPct)
	r.broadcast(buildWSMsg("game_complete", html))
}

func (r *Room) finalizeSideBySideGame(ctx context.Context, gs *game.GameState, room *game.Room, puzzle *game.Puzzle, userID string) {
	now := time.Now()
	elapsed := now.Sub(gs.StartedAt).Seconds()
	diff := gs.PuzzleRef.Difficulty
	number := gs.PuzzleRef.Number

	for i := range gs.Players {
		if gs.Players[i].UserID == userID {
			t := now
			gs.Players[i].FinishedAt = &t
			gs.Players[i].Finished = true
			score := gs.Players[i].Score // per-cell accumulated
			r.store.UpsertScore(ctx, userID, diff, number, score, int(elapsed))
			r.store.UpdateLeaderboard(ctx, diff, number, userID, gs.Players[i].DisplayName, score, int(elapsed))
			r.store.UpdateDifficultyLeaderboard(ctx, diff, userID, gs.Players[i].DisplayName, score, int(elapsed))
			break
		}
	}
	gs.Status = game.StatusComplete
	r.store.StoreGame(ctx, *gs)

	r.store.IncrPuzzleCompletion(ctx, diff, number)
	r.store.RecordCompletionTime(ctx, diff, number, gs.GameID, int(elapsed))
	stats, _ := r.store.GetPuzzleStats(ctx, diff, number)
	speedPct, _ := r.store.GetSpeedPercentile(ctx, diff, number, gs.GameID)

	leaderboard, _ := r.store.GetLeaderboard(ctx, diff, number, 10)
	html := r.renderGameComplete(gs, leaderboard, stats, speedPct)
	r.sendTo(userID, buildWSMsg("game_complete", html))
}

// --- rendering helpers ---

// PlayerInfo carries the display data needed by the player-status partial.
type PlayerInfo struct {
	DisplayName string
	Color       string
}

func (r *Room) renderPlayerStatus(room *game.Room) string {
	players := make([]PlayerInfo, 0, len(room.Players))
	for i, uid := range room.Players {
		color := game.PlayerColors[i%len(game.PlayerColors)]
		name := uid // fallback to ID if client not connected yet
		if c, ok := r.clients[uid]; ok {
			name = c.user.DisplayName
		}
		players = append(players, PlayerInfo{DisplayName: name, Color: color})
	}
	return r.renderPartial("partials/player_status.html", map[string]interface{}{
		"Room":    room,
		"Players": players,
	})
}

func (r *Room) renderGameStarted(gs *game.GameState, opponentGS *game.GameState, room *game.Room) string {
	data := map[string]interface{}{"Game": gs, "Opponent": opponentGS, "Room": room}
	if room.Mode == game.ModeSideBySide {
		return r.renderPageBlock("pages/sidebyside.html", "sidebyside-content", data)
	}
	return r.renderPageBlock("pages/shared_board.html", "shared-board-content", data)
}

func (r *Room) renderCell(row, col, val int, correct bool, ownerID string, gs *game.GameState, mode game.GameMode) string {
	return r.renderPartial("partials/cell.html", map[string]interface{}{
		"Row": row, "Col": col, "Value": val, "Correct": correct,
		"Owner": ownerID, "Game": gs, "Mode": string(mode), "OOB": true,
	})
}

func (r *Room) renderOpponentCell(row, col, val int) string {
	return r.renderPartial("partials/cell.html", map[string]interface{}{
		"Row": row, "Col": col, "Value": val,
		"Opponent": true, "OOB": true,
	})
}

func (r *Room) renderScorePanel(gs *game.GameState) string {
	return r.renderPartial("partials/score_panel.html", map[string]interface{}{
		"Game":   gs,
		"OOB":    true,
		"Collab": gs.Mode == game.ModeCollaborative,
	})
}

func (r *Room) renderOppScorePanel(gs *game.GameState) string {
	return r.renderPartial("partials/opp_score_panel.html", map[string]interface{}{"Game": gs, "OOB": true})
}

func (r *Room) renderGameComplete(gs *game.GameState, leaderboard []game.LeaderboardEntry, stats game.PuzzleStats, speedPct int) string {
	return r.renderPartial("partials/leaderboard_result.html", map[string]interface{}{
		"Game":            gs,
		"Leaderboard":     leaderboard,
		"OOB":             true,
		"SuccessRate":     stats.SuccessRate(),
		"TotalAttempts":   stats.Attempts,
		"SpeedPercentile": speedPct,
		"RoomCode":        r.Code,
	})
}

func (r *Room) renderPartial(name string, data interface{}) string {
	var buf bytes.Buffer
	if err := r.tmpl.RenderPartial(&buf, name, data); err != nil {
		log.Printf("template render error (%s): %v", name, err)
		return ""
	}
	return buf.String()
}

func (r *Room) renderPageBlock(pageName, blockName string, data interface{}) string {
	var buf bytes.Buffer
	if err := r.tmpl.RenderPageBlock(&buf, pageName, blockName, data); err != nil {
		log.Printf("template render error (%s/%s): %v", pageName, blockName, err)
		return ""
	}
	return buf.String()
}

// buildWSMsg marshals a server→client message to JSON.
func buildWSMsg(msgType, html string) []byte {
	b, _ := json.Marshal(OutboundMsg{Type: msgType, HTML: html})
	return b
}

// --- game state builders ---

func generateGameID() string {
	b := make([]byte, 16)
	cryptorand.Read(b)
	return hex.EncodeToString(b)
}

// randomPuzzleNumber returns a random puzzle number in [1, PuzzlesPerDifficulty].
func randomPuzzleNumber() int {
	return mathrand.Intn(game.PuzzlesPerDifficulty) + 1
}

// randomPuzzleNumberExcluding returns a random puzzle number different from exclude.
func randomPuzzleNumberExcluding(exclude int) int {
	for {
		n := mathrand.Intn(game.PuzzlesPerDifficulty) + 1
		if n != exclude {
			return n
		}
	}
}

func buildGameState(userID, username, displayName string, playerIdx int, puzzle game.Puzzle, mode game.GameMode) game.GameState {
	return game.GameState{
		GameID:     generateGameID(),
		PuzzleRef:  game.PuzzleRef{Difficulty: puzzle.Difficulty, Number: puzzle.Number},
		BoardState: puzzle.Grid,
		Mode:       mode,
		Players: []game.Player{
			{
				UserID:      userID,
				Username:    username,
				DisplayName: displayName,
				Color:       game.PlayerColors[playerIdx%len(game.PlayerColors)],
			},
		},
		CellOwners: make(map[string]string),
		StartedAt:  time.Now(),
		Status:     game.StatusPlaying,
	}
}

func buildSharedGameState(playerIDs []string, clients map[string]*Client, puzzle game.Puzzle, mode game.GameMode) game.GameState {
	players := make([]game.Player, 0, len(playerIDs))
	for i, uid := range playerIDs {
		c := clients[uid]
		if c == nil {
			continue
		}
		players = append(players, game.Player{
			UserID:      uid,
			Username:    c.user.Username,
			DisplayName: c.user.DisplayName,
			Color:       game.PlayerColors[i%len(game.PlayerColors)],
		})
	}
	return game.GameState{
		GameID:     generateGameID(),
		PuzzleRef:  game.PuzzleRef{Difficulty: puzzle.Difficulty, Number: puzzle.Number},
		BoardState: puzzle.Grid,
		Mode:       mode,
		Players:    players,
		CellOwners: make(map[string]string),
		StartedAt:  time.Now(),
		Status:     game.StatusPlaying,
	}
}
