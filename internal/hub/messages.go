package hub

// InboundMsg is a message received from a WebSocket client.
// HTMX sends form-encoded data; we parse it into this struct.
type InboundMsg struct {
	Type     string // "join_room", "cell_fill", "cell_clear", "start_game", "ttt_move", "c4_drop", "checkers_click", "ping"
	RoomID   string
	UserID   string
	Username string
	// cell_fill / cell_clear / ttt_move / checkers_click fields
	Row   int
	Col   int
	Value int // 0 = clear (cell_fill only)
	// start_game fields
	Mode       string
	Difficulty string
	Number     int
	GameType   string // "sudoku", "tictactoe", "connectfour", "checkers"
}

// OutboundMsg is a JSON message sent to WebSocket clients.
type OutboundMsg struct {
	Type string `json:"type"`
	HTML string `json:"html,omitempty"`
}
