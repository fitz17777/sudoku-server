package templates

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/user/sudoku/internal/game"
)

// FuncMap returns the custom template functions available to all templates.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i
			}
			return s
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mod": func(a, b int) int { return a % b },
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"formatTime": func(seconds int) string {
			m := seconds / 60
			s := seconds % 60
			return fmt.Sprintf("%d:%02d", m, s)
		},
		"difficultyLabel": game.DifficultyLabel,
		"basePoints": func(d game.Difficulty) int {
			return game.BasePoints(d)
		},
		"maxSeconds": func(d game.Difficulty) int {
			return int(game.DifficultyMaxSeconds(d))
		},
		"playerColor": func(color string) template.CSS {
			colors := map[string]string{
				"blue": "#3b82f6",
				"red":  "#ef4444",
			}
			if c, ok := colors[color]; ok {
				return template.CSS(c)
			}
			return template.CSS("#6b7280")
		},
		"cellID": func(row, col int) string {
			return fmt.Sprintf("cell-%d-%d", row, col)
		},
		"oppCellID": func(row, col int) string {
			return fmt.Sprintf("opp-cell-%d-%d", row, col)
		},
		"isThickBorderRight": func(col int) bool {
			return col == 2 || col == 5
		},
		"isThickBorderBottom": func(row int) bool {
			return row == 2 || row == 5
		},
		"puzzleNumbers": func() []int {
			nums := make([]int, game.PuzzlesPerDifficulty)
			for i := range nums {
				nums[i] = i + 1
			}
			return nums
		},
		"difficulties": func() []game.Difficulty {
			return game.Difficulties
		},
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s)
		},
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
		"toString": func(v interface{}) string {
			return fmt.Sprintf("%v", v)
		},
		"isModeCollaborative": func(m interface{}) bool {
			return fmt.Sprintf("%v", m) == string(game.ModeCollaborative)
		},
		"isModeCompetitive": func(m interface{}) bool {
			return fmt.Sprintf("%v", m) == string(game.ModeCompetitive)
		},
		"isModeSideBySide": func(m interface{}) bool {
			return fmt.Sprintf("%v", m) == string(game.ModeSideBySide)
		},

		// --- Mini-game helpers ---

		// tttSymbol converts a board value (0/1/2) to "", "X", or "O".
		"tttSymbol": func(v int) string {
			switch v {
			case 1:
				return "X"
			case 2:
				return "O"
			}
			return ""
		},

		// isTTTWinCell returns true if (r,c) is in the winning line.
		"isTTTWinCell": func(winLine [][2]int, r, c int) bool {
			for _, cell := range winLine {
				if cell[0] == r && cell[1] == c {
					return true
				}
			}
			return false
		},

		// isWinCell returns true if (r,c) is in a connect-four winning cells list.
		"isWinCell": func(winCells [][2]int, r, c int) bool {
			for _, cell := range winCells {
				if cell[0] == r && cell[1] == c {
					return true
				}
			}
			return false
		},

		// c4PlayerClass maps 0/1/2 to "empty"/"p1"/"p2".
		"c4PlayerClass": func(v int) string {
			switch v {
			case 1:
				return "p1"
			case 2:
				return "p2"
			}
			return "empty"
		},

		// isDarkSquare returns true for dark (playable) checkers squares.
		"isDarkSquare": func(r, c int) bool {
			return (r+c)%2 == 1
		},

		// checkersPieceClass maps board value to a CSS class.
		"checkersPieceClass": func(v int) string {
			switch v {
			case 1:
				return "p1-piece"
			case 2:
				return "p2-piece"
			case 3:
				return "p1-king"
			case 4:
				return "p2-king"
			}
			return ""
		},

		// isSelected returns true if (r,c) is the currently selected checkers piece.
		"isSelected": func(selected *[2]int, r, c int) bool {
			return selected != nil && selected[0] == r && selected[1] == c
		},

		// isValidDest returns true if (r,c) is a valid move destination.
		"isValidDest": func(validMoves []game.CheckersMove, r, c int) bool {
			for _, m := range validMoves {
				if m.ToRow == r && m.ToCol == c {
					return true
				}
			}
			return false
		},

		// gameTypeLabel maps a game type string to a display name.
		"gameTypeLabel": func(gt string) string {
			switch gt {
			case game.GameTypeTicTacToe:
				return "Tic Tac Toe"
			case game.GameTypeConnectFour:
				return "Connect Four"
			case game.GameTypeCheckers:
				return "Checkers"
			}
			return "Sudoku"
		},
	}
}
