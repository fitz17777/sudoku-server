package game

import "time"

// Difficulty levels.
type Difficulty string

const (
	Easy   Difficulty = "easy"
	Medium Difficulty = "medium"
	Hard   Difficulty = "hard"
	Expert Difficulty = "expert"
	Master Difficulty = "master"
)

var Difficulties = []Difficulty{Easy, Medium, Hard, Expert, Master}

// DifficultyLabel returns a human-readable label.
func DifficultyLabel(d Difficulty) string {
	switch d {
	case Easy:
		return "Easy"
	case Medium:
		return "Medium"
	case Hard:
		return "Hard"
	case Expert:
		return "Expert"
	case Master:
		return "Master"
	}
	return string(d)
}

// DifficultyClueRange returns [min, max] clue counts for a difficulty.
func DifficultyClueRange(d Difficulty) (int, int) {
	switch d {
	case Easy:
		return 45, 50
	case Medium:
		return 36, 44
	case Hard:
		return 28, 35
	case Expert:
		return 22, 27
	case Master:
		return 17, 21
	}
	return 30, 35
}

// DifficultyMaxSeconds returns the time limit in seconds for scoring.
func DifficultyMaxSeconds(d Difficulty) float64 {
	switch d {
	case Easy:
		return 1800
	case Medium:
		return 2400
	case Hard:
		return 3000
	case Expert:
		return 3600
	case Master:
		return 4200
	}
	return 3000
}

const PuzzlesPerDifficulty = 25

// Puzzle represents a Sudoku puzzle stored in Redis.
type Puzzle struct {
	Difficulty Difficulty `json:"difficulty"`
	Number     int        `json:"number"`
	Grid       [9][9]int  `json:"grid"`     // 0 = empty clue cell
	Solution   [9][9]int  `json:"solution"`  // fully solved grid
	ClueCount  int        `json:"clue_count"`
}

// GameType identifies which game is played in a room.
const (
	GameTypeSudoku      = "sudoku"
	GameTypeTicTacToe   = "tictactoe"
	GameTypeConnectFour = "connectfour"
	GameTypeCheckers    = "checkers"
)

// GameMode for multiplayer.
type GameMode string

const (
	ModeSolo          GameMode = "solo"
	ModeCollaborative GameMode = "collaborative"
	ModeCompetitive   GameMode = "competitive"
	ModeSideBySide    GameMode = "sidebyside"
)

// GameStatus tracks the lifecycle of a game.
type GameStatus string

const (
	StatusWaiting  GameStatus = "waiting"
	StatusPlaying  GameStatus = "playing"
	StatusComplete GameStatus = "complete"
	StatusFailed   GameStatus = "failed"
)

// Player holds per-player state inside a game.
type Player struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Color       string `json:"color"`
	Score       int    `json:"score"`
	Mistakes    int    `json:"mistakes"`
	Finished    bool   `json:"finished"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// GameState is the full in-Redis representation of an active game.
type GameState struct {
	GameID     string            `json:"game_id"`
	PuzzleRef  PuzzleRef         `json:"puzzle_ref"`
	BoardState [9][9]int         `json:"board_state"`
	Mode       GameMode          `json:"mode"`
	Players    []Player          `json:"players"`
	CellOwners map[string]string `json:"cell_owners"` // "row,col" → user_id (competitive)
	StartedAt  time.Time         `json:"started_at"`
	Status     GameStatus        `json:"status"`
}

// PuzzleRef is a lightweight reference to a puzzle.
type PuzzleRef struct {
	Difficulty Difficulty `json:"difficulty"`
	Number     int        `json:"number"`
}

// Room represents a multiplayer room in Redis.
type Room struct {
	Code    string     `json:"code"`
	GameIDs []string   `json:"game_ids"`
	Players []string   `json:"players"` // user IDs
	Status  GameStatus `json:"status"`
	Mode    GameMode   `json:"mode"`
	// Difficulty and puzzle number are set at lobby time
	Difficulty Difficulty `json:"difficulty"`
	Number     int        `json:"number"`
	HostUserID string     `json:"host_user_id"`
	GameType   string     `json:"game_type"` // empty = "sudoku" for backward compat
}

// User is the authenticated user injected into request context.
type User struct {
	UserID      string
	Username    string
	DisplayName string
}

// ScoreRecord tracks a player's best performance on a specific puzzle.
type ScoreRecord struct {
	BestScore  int       `json:"best_score"`
	BestTimeS  int       `json:"best_time_s"`
	Attempts   int       `json:"attempts"`
	LastPlayed time.Time `json:"last_played"`
}

// LeaderboardEntry is one row in a puzzle leaderboard.
type LeaderboardEntry struct {
	Rank        int
	UserID      string
	Username    string // kept for compat; prefer DisplayName for display
	DisplayName string // human-readable name (may be "Alice & Bob" for collab)
	Score       int
	BestTimeS   int
}

// LeaderboardMeta is the display data stored alongside a leaderboard sorted-set key.
type LeaderboardMeta struct {
	DisplayName    string `json:"display_name"`
	ElapsedSeconds int    `json:"elapsed_seconds"`
}

// PuzzleStats tracks attempt and completion counts for a puzzle.
type PuzzleStats struct {
	Attempts    int64
	Completions int64
}

// SuccessRate returns the percentage of attempts that were completed (0–100).
func (s PuzzleStats) SuccessRate() int {
	if s.Attempts == 0 {
		return 0
	}
	return int(float64(s.Completions) / float64(s.Attempts) * 100)
}

// DifficultyLeaderboardEntry is one row in a difficulty-level leaderboard.
type DifficultyLeaderboardEntry struct {
	Rank        int
	DisplayName string
	BestScore   int
	BestTimeS   int
}

// PlayerColors for multiplayer color assignment.
var PlayerColors = []string{"blue", "red"}

// --- Turn-based mini-games ---

// TicTacToeState is the in-Redis state for a tic-tac-toe game.
type TicTacToeState struct {
	GameID    string     `json:"game_id"`
	RoomCode  string     `json:"room_code"`
	Board     [3][3]int  `json:"board"`    // 0=empty 1=P1(X) 2=P2(O)
	Players   []Player   `json:"players"`
	Turn      string     `json:"turn"`     // userID of current player
	Status    GameStatus `json:"status"`
	WinnerID  string     `json:"winner_id"`
	WinLine   [][2]int   `json:"win_line"` // three winning cells, nil if none
	StartedAt time.Time  `json:"started_at"`
}

// ConnectFourState is the in-Redis state for a Connect Four game.
// Board rows 0–5 rendered top-to-bottom; pieces fall to highest row index.
type ConnectFourState struct {
	GameID    string     `json:"game_id"`
	RoomCode  string     `json:"room_code"`
	Board     [6][7]int  `json:"board"`     // 0=empty 1=P1 2=P2
	Players   []Player   `json:"players"`
	Turn      string     `json:"turn"`
	Status    GameStatus `json:"status"`
	WinnerID  string     `json:"winner_id"`
	WinCells  [][2]int   `json:"win_cells"` // four winning cells, nil if none
	StartedAt time.Time  `json:"started_at"`
}

// CheckersMove describes one legal move or jump for a checkers piece.
type CheckersMove struct {
	ToRow  int  `json:"to_row"`
	ToCol  int  `json:"to_col"`
	IsJump bool `json:"is_jump"`
	CapRow int  `json:"cap_row"` // captured piece row (jump only)
	CapCol int  `json:"cap_col"` // captured piece col (jump only)
}

// CheckersState is the in-Redis state for a Checkers game.
// Board: 0=empty 1=P1 2=P2 3=P1king 4=P2king
// P1 starts rows 5-7 (moves toward row 0); P2 starts rows 0-2 (moves toward row 7).
type CheckersState struct {
	GameID       string        `json:"game_id"`
	RoomCode     string        `json:"room_code"`
	Board        [8][8]int     `json:"board"`
	Players      []Player      `json:"players"`
	Turn         string        `json:"turn"`
	Status       GameStatus    `json:"status"`
	WinnerID     string        `json:"winner_id"`
	Selected     *[2]int       `json:"selected"`       // currently selected piece, nil if none
	MustJumpFrom *[2]int       `json:"must_jump_from"` // mid-multi-jump, nil otherwise
	StartedAt    time.Time     `json:"started_at"`
}

// WinLeaderboardEntry is one row in a wins-based leaderboard (mini-games).
type WinLeaderboardEntry struct {
	Rank        int
	DisplayName string
	Wins        int
}
