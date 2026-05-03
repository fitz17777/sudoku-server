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
	case "ttt_move":
		r.handleTicTacToeMove(ctx, msg)
	case "c4_drop":
		r.handleConnectFourDrop(ctx, msg)
	case "checkers_click":
		r.handleCheckersClick(ctx, msg)
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

	// Store game type in room (default to sudoku for empty/unknown)
	gt := msg.GameType
	if gt == "" {
		gt = game.GameTypeSudoku
	}
	room.GameType = gt

	switch gt {
	case game.GameTypeTicTacToe:
		r.startTicTacToe(ctx, room, msg)
	case game.GameTypeConnectFour:
		r.startConnectFour(ctx, room, msg)
	case game.GameTypeCheckers:
		r.startCheckers(ctx, room, msg)
	default:
		r.startSudoku(ctx, room, msg)
	}
}

func (r *Room) startSudoku(ctx context.Context, room *game.Room, msg InboundMsg) {
	diff := game.Difficulty(msg.Difficulty)
	room.Difficulty = diff
	room.Mode = game.GameMode(msg.Mode)
	room.Status = game.StatusPlaying

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

// ============================================================
// Tic Tac Toe
// ============================================================

func (r *Room) startTicTacToe(ctx context.Context, room *game.Room, msg InboundMsg) {
	if len(room.Players) < 2 {
		return
	}
	room.Status = game.StatusPlaying
	gameID := generateGameID()
	room.GameIDs = []string{gameID}
	if err := r.store.StoreRoom(ctx, *room); err != nil {
		log.Printf("room %s: StoreRoom error: %v", r.Code, err)
		return
	}

	players := buildMiniGamePlayers(room.Players, r.clients)
	gs := &game.TicTacToeState{
		GameID:    gameID,
		RoomCode:  r.Code,
		Players:   players,
		Turn:      room.Players[0],
		Status:    game.StatusPlaying,
		StartedAt: time.Now(),
	}
	if err := r.store.StoreTicTacToeState(ctx, gs); err != nil {
		log.Printf("room %s: StoreTicTacToeState error: %v", r.Code, err)
		return
	}

	turnName := tttTurnName(gs)
	html := r.renderPageBlock("pages/tictactoe.html", "tictactoe-content", map[string]interface{}{
		"State":    gs,
		"Room":     room,
		"TurnName": turnName,
	})
	r.broadcast(buildWSMsg("game_started", html))
}

func (r *Room) handleTicTacToeMove(ctx context.Context, msg InboundMsg) {
	room, err := r.store.GetRoom(ctx, r.Code)
	if err != nil || room.Status != game.StatusPlaying || len(room.GameIDs) == 0 {
		return
	}
	gs, err := r.store.GetTicTacToeState(ctx, room.GameIDs[0])
	if err != nil || gs.Status != game.StatusPlaying || gs.Turn != msg.UserID {
		return
	}
	row, col := msg.Row, msg.Col
	if row < 0 || row > 2 || col < 0 || col > 2 || gs.Board[row][col] != 0 {
		return
	}

	playerIdx := 0
	for i, p := range gs.Players {
		if p.UserID == msg.UserID {
			playerIdx = i
			break
		}
	}
	gs.Board[row][col] = playerIdx + 1

	winner, winLine := checkTTTWin(gs.Board)
	if winner > 0 {
		gs.WinLine = winLine
		gs.WinnerID = gs.Players[winner-1].UserID
		gs.Status = game.StatusComplete
		r.store.StoreTicTacToeState(ctx, gs)
		r.finalizeTicTacToeGame(ctx, gs, room)
		return
	}
	if isTTTDraw(gs.Board) {
		gs.Status = game.StatusComplete
		r.store.StoreTicTacToeState(ctx, gs)
		r.finalizeTicTacToeGame(ctx, gs, room)
		return
	}

	// Flip turn
	for _, p := range gs.Players {
		if p.UserID != msg.UserID {
			gs.Turn = p.UserID
			break
		}
	}
	r.store.StoreTicTacToeState(ctx, gs)

	html := r.renderTicTacToeUpdate(gs)
	r.broadcast(buildWSMsg("board_update", html))
}

func (r *Room) finalizeTicTacToeGame(ctx context.Context, gs *game.TicTacToeState, room *game.Room) {
	room.Status = game.StatusComplete
	r.store.StoreRoom(ctx, *room)

	// Record win
	if gs.WinnerID != "" {
		for _, p := range gs.Players {
			if p.UserID == gs.WinnerID {
				r.store.RecordWin(ctx, game.GameTypeTicTacToe, p.UserID, p.DisplayName)
				break
			}
		}
	}

	html := r.renderMiniGameResult(ctx, gs.Players, gs.WinnerID, game.GameTypeTicTacToe, gs.WinLine, gs.Board, [6][7]int{})
	r.broadcast(buildWSMsg("game_complete", html))
}

func checkTTTWin(board [3][3]int) (int, [][2]int) {
	lines := [][3][2]int{
		{{0, 0}, {0, 1}, {0, 2}},
		{{1, 0}, {1, 1}, {1, 2}},
		{{2, 0}, {2, 1}, {2, 2}},
		{{0, 0}, {1, 0}, {2, 0}},
		{{0, 1}, {1, 1}, {2, 1}},
		{{0, 2}, {1, 2}, {2, 2}},
		{{0, 0}, {1, 1}, {2, 2}},
		{{0, 2}, {1, 1}, {2, 0}},
	}
	for _, line := range lines {
		v := board[line[0][0]][line[0][1]]
		if v != 0 && v == board[line[1][0]][line[1][1]] && v == board[line[2][0]][line[2][1]] {
			cells := [][2]int{{line[0][0], line[0][1]}, {line[1][0], line[1][1]}, {line[2][0], line[2][1]}}
			return v, cells
		}
	}
	return 0, nil
}

func isTTTDraw(board [3][3]int) bool {
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			if board[r][c] == 0 {
				return false
			}
		}
	}
	return true
}

func tttTurnName(gs *game.TicTacToeState) string {
	if gs.Status != game.StatusPlaying {
		return ""
	}
	for _, p := range gs.Players {
		if p.UserID == gs.Turn {
			return p.DisplayName + "'s turn"
		}
	}
	return ""
}

// ============================================================
// Connect Four
// ============================================================

func (r *Room) startConnectFour(ctx context.Context, room *game.Room, msg InboundMsg) {
	if len(room.Players) < 2 {
		return
	}
	room.Status = game.StatusPlaying
	gameID := generateGameID()
	room.GameIDs = []string{gameID}
	if err := r.store.StoreRoom(ctx, *room); err != nil {
		log.Printf("room %s: StoreRoom error: %v", r.Code, err)
		return
	}

	players := buildMiniGamePlayers(room.Players, r.clients)
	gs := &game.ConnectFourState{
		GameID:    gameID,
		RoomCode:  r.Code,
		Players:   players,
		Turn:      room.Players[0],
		Status:    game.StatusPlaying,
		StartedAt: time.Now(),
	}
	if err := r.store.StoreConnectFourState(ctx, gs); err != nil {
		log.Printf("room %s: StoreConnectFourState error: %v", r.Code, err)
		return
	}

	html := r.renderPageBlock("pages/connectfour.html", "connectfour-content", map[string]interface{}{
		"State":    gs,
		"Room":     room,
		"TurnName": c4TurnName(gs),
	})
	r.broadcast(buildWSMsg("game_started", html))
}

func (r *Room) handleConnectFourDrop(ctx context.Context, msg InboundMsg) {
	room, err := r.store.GetRoom(ctx, r.Code)
	if err != nil || room.Status != game.StatusPlaying || len(room.GameIDs) == 0 {
		return
	}
	gs, err := r.store.GetConnectFourState(ctx, room.GameIDs[0])
	if err != nil || gs.Status != game.StatusPlaying || gs.Turn != msg.UserID {
		return
	}
	col := msg.Col
	if col < 0 || col > 6 {
		return
	}

	// Find lowest empty row (pieces fall to highest row index)
	row := -1
	for rr := 5; rr >= 0; rr-- {
		if gs.Board[rr][col] == 0 {
			row = rr
			break
		}
	}
	if row < 0 {
		return // column full
	}

	playerIdx := 0
	for i, p := range gs.Players {
		if p.UserID == msg.UserID {
			playerIdx = i
			break
		}
	}
	gs.Board[row][col] = playerIdx + 1

	winner, winCells := checkC4Win(gs.Board)
	if winner > 0 {
		gs.WinCells = winCells
		gs.WinnerID = gs.Players[winner-1].UserID
		gs.Status = game.StatusComplete
		r.store.StoreConnectFourState(ctx, gs)
		r.finalizeConnectFourGame(ctx, gs, room)
		return
	}
	if isC4Draw(gs.Board) {
		gs.Status = game.StatusComplete
		r.store.StoreConnectFourState(ctx, gs)
		r.finalizeConnectFourGame(ctx, gs, room)
		return
	}

	// Flip turn
	for _, p := range gs.Players {
		if p.UserID != msg.UserID {
			gs.Turn = p.UserID
			break
		}
	}
	r.store.StoreConnectFourState(ctx, gs)

	html := r.renderConnectFourUpdate(gs)
	r.broadcast(buildWSMsg("board_update", html))
}

func (r *Room) finalizeConnectFourGame(ctx context.Context, gs *game.ConnectFourState, room *game.Room) {
	room.Status = game.StatusComplete
	r.store.StoreRoom(ctx, *room)

	if gs.WinnerID != "" {
		for _, p := range gs.Players {
			if p.UserID == gs.WinnerID {
				r.store.RecordWin(ctx, game.GameTypeConnectFour, p.UserID, p.DisplayName)
				break
			}
		}
	}

	// Send final board state (with win cells highlighted) then result
	boardHTML := r.renderConnectFourUpdate(gs)
	r.broadcast(buildWSMsg("board_update", boardHTML))

	html := r.renderMiniGameResult(ctx, gs.Players, gs.WinnerID, game.GameTypeConnectFour, nil, [3][3]int{}, gs.Board)
	r.broadcast(buildWSMsg("game_complete", html))
}

func checkC4Win(board [6][7]int) (int, [][2]int) {
	dirs := [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for row := 0; row < 6; row++ {
		for col := 0; col < 7; col++ {
			v := board[row][col]
			if v == 0 {
				continue
			}
			for _, d := range dirs {
				cells := [][2]int{{row, col}}
				for k := 1; k < 4; k++ {
					nr, nc := row+k*d[0], col+k*d[1]
					if nr < 0 || nr >= 6 || nc < 0 || nc >= 7 || board[nr][nc] != v {
						break
					}
					cells = append(cells, [2]int{nr, nc})
				}
				if len(cells) == 4 {
					return v, cells
				}
			}
		}
	}
	return 0, nil
}

func isC4Draw(board [6][7]int) bool {
	for c := 0; c < 7; c++ {
		if board[0][c] == 0 {
			return false
		}
	}
	return true
}

func c4TurnName(gs *game.ConnectFourState) string {
	if gs.Status != game.StatusPlaying {
		return ""
	}
	for _, p := range gs.Players {
		if p.UserID == gs.Turn {
			return p.DisplayName + "'s turn"
		}
	}
	return ""
}

// ============================================================
// Checkers
// ============================================================

func (r *Room) startCheckers(ctx context.Context, room *game.Room, msg InboundMsg) {
	if len(room.Players) < 2 {
		return
	}
	room.Status = game.StatusPlaying
	gameID := generateGameID()
	room.GameIDs = []string{gameID}
	if err := r.store.StoreRoom(ctx, *room); err != nil {
		log.Printf("room %s: StoreRoom error: %v", r.Code, err)
		return
	}

	players := buildMiniGamePlayers(room.Players, r.clients)
	gs := &game.CheckersState{
		GameID:    gameID,
		RoomCode:  r.Code,
		Players:   players,
		Turn:      room.Players[0],
		Status:    game.StatusPlaying,
		StartedAt: time.Now(),
		Board:     newCheckersBoard(),
	}
	if err := r.store.StoreCheckersState(ctx, gs); err != nil {
		log.Printf("room %s: StoreCheckersState error: %v", r.Code, err)
		return
	}

	html := r.renderPageBlock("pages/checkers.html", "checkers-content", map[string]interface{}{
		"State":      gs,
		"Room":       room,
		"TurnName":   checkersTurnName(gs),
		"ValidMoves": []game.CheckersMove{},
	})
	r.broadcast(buildWSMsg("game_started", html))
}

func (r *Room) handleCheckersClick(ctx context.Context, msg InboundMsg) {
	room, err := r.store.GetRoom(ctx, r.Code)
	if err != nil || room.Status != game.StatusPlaying || len(room.GameIDs) == 0 {
		return
	}
	gs, err := r.store.GetCheckersState(ctx, room.GameIDs[0])
	if err != nil || gs.Status != game.StatusPlaying || gs.Turn != msg.UserID {
		return
	}

	clickR, clickC := msg.Row, msg.Col
	playerIdx := 0
	for i, p := range gs.Players {
		if p.UserID == msg.UserID {
			playerIdx = i
			break
		}
	}

	if gs.MustJumpFrom != nil {
		// Mid-multi-jump: only the forced piece can move and only as a jump
		fromR, fromC := gs.MustJumpFrom[0], gs.MustJumpFrom[1]
		jumps := checkersJumpsForPiece(gs.Board, fromR, fromC, playerIdx)
		var chosen *game.CheckersMove
		for i, m := range jumps {
			if m.ToRow == clickR && m.ToCol == clickC {
				chosen = &jumps[i]
				break
			}
		}
		if chosen == nil {
			// Invalid — re-broadcast board with existing selection
			r.broadcastCheckersBoard(gs, jumps)
			return
		}
		r.executeCheckersJump(ctx, gs, room, chosen, fromR, fromC, playerIdx)
		return
	}

	if gs.Selected == nil {
		// First click — select a piece
		v := gs.Board[clickR][clickC]
		if !checkersIsOwnPiece(v, playerIdx) {
			return
		}
		moves := checkersValidMovesForPiece(gs.Board, clickR, clickC, playerIdx)
		if len(moves) == 0 {
			return
		}
		sel := [2]int{clickR, clickC}
		gs.Selected = &sel
		r.store.StoreCheckersState(ctx, gs)
		r.broadcastCheckersBoard(gs, moves)
		return
	}

	// Second click — move or re-select
	selR, selC := gs.Selected[0], gs.Selected[1]

	if clickR == selR && clickC == selC {
		// Deselect
		gs.Selected = nil
		r.store.StoreCheckersState(ctx, gs)
		r.broadcastCheckersBoard(gs, nil)
		return
	}

	// Re-select own piece?
	if v := gs.Board[clickR][clickC]; checkersIsOwnPiece(v, playerIdx) {
		moves := checkersValidMovesForPiece(gs.Board, clickR, clickC, playerIdx)
		if len(moves) > 0 {
			sel := [2]int{clickR, clickC}
			gs.Selected = &sel
			r.store.StoreCheckersState(ctx, gs)
			r.broadcastCheckersBoard(gs, moves)
			return
		}
	}

	// Attempt move to destination
	moves := checkersValidMovesForPiece(gs.Board, selR, selC, playerIdx)
	var chosen *game.CheckersMove
	for i, m := range moves {
		if m.ToRow == clickR && m.ToCol == clickC {
			chosen = &moves[i]
			break
		}
	}
	if chosen == nil {
		// Invalid destination — deselect
		gs.Selected = nil
		r.store.StoreCheckersState(ctx, gs)
		r.broadcastCheckersBoard(gs, nil)
		return
	}

	if chosen.IsJump {
		r.executeCheckersJump(ctx, gs, room, chosen, selR, selC, playerIdx)
	} else {
		r.executeCheckersRegularMove(ctx, gs, room, chosen, selR, selC, playerIdx)
	}
}

func (r *Room) executeCheckersRegularMove(ctx context.Context, gs *game.CheckersState, room *game.Room, m *game.CheckersMove, fromR, fromC, playerIdx int) {
	gs.Board[m.ToRow][m.ToCol] = gs.Board[fromR][fromC]
	gs.Board[fromR][fromC] = 0
	checkersPromoteKing(&gs.Board, m.ToRow, m.ToCol, playerIdx)
	gs.Selected = nil
	gs.MustJumpFrom = nil

	// Flip turn
	for _, p := range gs.Players {
		if p.UserID != gs.Turn {
			gs.Turn = p.UserID
			break
		}
	}
	r.store.StoreCheckersState(ctx, gs)

	if winnerID := checkersWinner(gs); winnerID != "" {
		gs.WinnerID = winnerID
		gs.Status = game.StatusComplete
		r.store.StoreCheckersState(ctx, gs)
		r.finalizeCheckersGame(ctx, gs, room)
		return
	}
	r.broadcastCheckersBoard(gs, nil)
}

func (r *Room) executeCheckersJump(ctx context.Context, gs *game.CheckersState, room *game.Room, m *game.CheckersMove, fromR, fromC, playerIdx int) {
	gs.Board[m.ToRow][m.ToCol] = gs.Board[fromR][fromC]
	gs.Board[fromR][fromC] = 0
	gs.Board[m.CapRow][m.CapCol] = 0
	checkersPromoteKing(&gs.Board, m.ToRow, m.ToCol, playerIdx)

	// Check for continuation jump
	moreJumps := checkersJumpsForPiece(gs.Board, m.ToRow, m.ToCol, playerIdx)
	if len(moreJumps) > 0 {
		landing := [2]int{m.ToRow, m.ToCol}
		gs.MustJumpFrom = &landing
		gs.Selected = &landing
		r.store.StoreCheckersState(ctx, gs)
		r.broadcastCheckersBoard(gs, moreJumps)
		return
	}

	gs.MustJumpFrom = nil
	gs.Selected = nil
	for _, p := range gs.Players {
		if p.UserID != gs.Turn {
			gs.Turn = p.UserID
			break
		}
	}
	r.store.StoreCheckersState(ctx, gs)

	if winnerID := checkersWinner(gs); winnerID != "" {
		gs.WinnerID = winnerID
		gs.Status = game.StatusComplete
		r.store.StoreCheckersState(ctx, gs)
		r.finalizeCheckersGame(ctx, gs, room)
		return
	}
	r.broadcastCheckersBoard(gs, nil)
}

func (r *Room) finalizeCheckersGame(ctx context.Context, gs *game.CheckersState, room *game.Room) {
	room.Status = game.StatusComplete
	r.store.StoreRoom(ctx, *room)

	if gs.WinnerID != "" {
		for _, p := range gs.Players {
			if p.UserID == gs.WinnerID {
				r.store.RecordWin(ctx, game.GameTypeCheckers, p.UserID, p.DisplayName)
				break
			}
		}
	}

	r.broadcastCheckersBoard(gs, nil) // final board state
	html := r.renderMiniGameResult(ctx, gs.Players, gs.WinnerID, game.GameTypeCheckers, nil, [3][3]int{}, [6][7]int{})
	r.broadcast(buildWSMsg("game_complete", html))
}

func checkersWinner(gs *game.CheckersState) string {
	p1, p2 := 0, 0
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			v := gs.Board[r][c]
			if v == 1 || v == 3 {
				p1++
			} else if v == 2 || v == 4 {
				p2++
			}
		}
	}
	if p1 == 0 && len(gs.Players) > 1 {
		return gs.Players[1].UserID
	}
	if p2 == 0 && len(gs.Players) > 0 {
		return gs.Players[0].UserID
	}

	// Check if the next-to-move player has any legal moves
	turnIdx := 0
	for i, p := range gs.Players {
		if p.UserID == gs.Turn {
			turnIdx = i
			break
		}
	}
	if !checkersHasAnyMove(gs.Board, turnIdx) {
		winnerIdx := 1 - turnIdx
		if winnerIdx >= 0 && winnerIdx < len(gs.Players) {
			return gs.Players[winnerIdx].UserID
		}
	}
	return ""
}

func checkersValidMovesForPiece(board [8][8]int, row, col, playerIdx int) []game.CheckersMove {
	// Forced jump rule: if any piece can jump, must jump
	if checkersAnyJumpAvailable(board, playerIdx) {
		return checkersJumpsForPiece(board, row, col, playerIdx)
	}
	moves := checkersRegularMovesForPiece(board, row, col, playerIdx)
	moves = append(moves, checkersJumpsForPiece(board, row, col, playerIdx)...)
	return moves
}

func checkersRegularMovesForPiece(board [8][8]int, row, col, playerIdx int) []game.CheckersMove {
	v := board[row][col]
	dirs := checkersDirs(v, playerIdx)
	var moves []game.CheckersMove
	for _, d := range dirs {
		nr, nc := row+d[0], col+d[1]
		if nr >= 0 && nr < 8 && nc >= 0 && nc < 8 && board[nr][nc] == 0 {
			moves = append(moves, game.CheckersMove{ToRow: nr, ToCol: nc})
		}
	}
	return moves
}

func checkersJumpsForPiece(board [8][8]int, row, col, playerIdx int) []game.CheckersMove {
	v := board[row][col]
	dirs := checkersDirs(v, playerIdx)
	var jumps []game.CheckersMove
	for _, d := range dirs {
		capR, capC := row+d[0], col+d[1]
		landR, landC := row+2*d[0], col+2*d[1]
		if capR < 0 || capR >= 8 || capC < 0 || capC >= 8 {
			continue
		}
		if landR < 0 || landR >= 8 || landC < 0 || landC >= 8 {
			continue
		}
		if checkersIsOpponentPiece(board[capR][capC], playerIdx) && board[landR][landC] == 0 {
			jumps = append(jumps, game.CheckersMove{
				ToRow: landR, ToCol: landC,
				IsJump: true, CapRow: capR, CapCol: capC,
			})
		}
	}
	return jumps
}

func checkersAnyJumpAvailable(board [8][8]int, playerIdx int) bool {
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if checkersIsOwnPiece(board[r][c], playerIdx) {
				if len(checkersJumpsForPiece(board, r, c, playerIdx)) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func checkersHasAnyMove(board [8][8]int, playerIdx int) bool {
	for r := 0; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if checkersIsOwnPiece(board[r][c], playerIdx) {
				if len(checkersRegularMovesForPiece(board, r, c, playerIdx)) > 0 ||
					len(checkersJumpsForPiece(board, r, c, playerIdx)) > 0 {
					return true
				}
			}
		}
	}
	return false
}

func checkersDirs(pieceVal, playerIdx int) [][2]int {
	isKing := pieceVal == 3 || pieceVal == 4
	if isKing {
		return [][2]int{{-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	}
	if playerIdx == 0 { // P1 moves up (decreasing row)
		return [][2]int{{-1, -1}, {-1, 1}}
	}
	return [][2]int{{1, -1}, {1, 1}} // P2 moves down
}

func checkersIsOwnPiece(v, playerIdx int) bool {
	if playerIdx == 0 {
		return v == 1 || v == 3
	}
	return v == 2 || v == 4
}

func checkersIsOpponentPiece(v, playerIdx int) bool {
	if playerIdx == 0 {
		return v == 2 || v == 4
	}
	return v == 1 || v == 3
}

func checkersPromoteKing(board *[8][8]int, row, col, playerIdx int) {
	if playerIdx == 0 && row == 0 && board[row][col] == 1 {
		board[row][col] = 3
	}
	if playerIdx == 1 && row == 7 && board[row][col] == 2 {
		board[row][col] = 4
	}
}

func newCheckersBoard() [8][8]int {
	var b [8][8]int
	// P2 rows 0-2 on dark squares ((r+c)%2==1)
	for r := 0; r < 3; r++ {
		for c := 0; c < 8; c++ {
			if (r+c)%2 == 1 {
				b[r][c] = 2
			}
		}
	}
	// P1 rows 5-7 on dark squares
	for r := 5; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if (r+c)%2 == 1 {
				b[r][c] = 1
			}
		}
	}
	return b
}

func checkersTurnName(gs *game.CheckersState) string {
	if gs.Status != game.StatusPlaying {
		return ""
	}
	for _, p := range gs.Players {
		if p.UserID == gs.Turn {
			return p.DisplayName + "'s turn"
		}
	}
	return ""
}

// ============================================================
// Shared mini-game helpers
// ============================================================

type miniGameResultPlayer struct {
	DisplayName string
	Color       string
	Wins        int
}

func (r *Room) renderMiniGameResult(ctx context.Context, players []game.Player, winnerID, gameType string, tttWinLine [][2]int, tttBoard [3][3]int, c4Board [6][7]int) string {
	type resultData struct {
		OOB        bool
		IsDraw     bool
		WinnerName string
		Players    []miniGameResultPlayer
		RoomCode   string
	}
	data := resultData{OOB: true, RoomCode: r.Code}
	if winnerID == "" {
		data.IsDraw = true
	}

	// Build per-player win counts
	lb, _ := r.store.GetWinLeaderboard(ctx, gameType, 20)
	winMap := map[string]int{}
	for _, e := range lb {
		winMap[e.DisplayName] = e.Wins
	}

	for _, p := range players {
		if p.UserID == winnerID {
			data.WinnerName = p.DisplayName
		}
		data.Players = append(data.Players, miniGameResultPlayer{
			DisplayName: p.DisplayName,
			Color:       p.Color,
			Wins:        winMap[p.DisplayName],
		})
	}

	return r.renderPartial("partials/game_result.html", data)
}

func (r *Room) broadcastCheckersBoard(gs *game.CheckersState, validMoves []game.CheckersMove) {
	if validMoves == nil {
		validMoves = []game.CheckersMove{}
	}
	html := r.renderPartial("partials/checkers_update.html", map[string]interface{}{
		"Board":      gs.Board,
		"Selected":   gs.Selected,
		"ValidMoves": validMoves,
		"TurnName":   checkersTurnName(gs),
	})
	r.broadcast(buildWSMsg("board_update", html))
}

func (r *Room) renderTicTacToeUpdate(gs *game.TicTacToeState) string {
	return r.renderPartial("partials/ttt_update.html", map[string]interface{}{
		"Board":    gs.Board,
		"WinLine":  gs.WinLine,
		"TurnName": tttTurnName(gs),
	})
}

func (r *Room) renderConnectFourUpdate(gs *game.ConnectFourState) string {
	return r.renderPartial("partials/c4_update.html", map[string]interface{}{
		"Board":    gs.Board,
		"WinCells": gs.WinCells,
		"TurnName": c4TurnName(gs),
	})
}

func buildMiniGamePlayers(playerIDs []string, clients map[string]*Client) []game.Player {
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
	return players
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
