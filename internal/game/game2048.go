package game

import "math/rand"

// Move2048 applies a directional move to a 2048 board.
// Returns the new board, score gained, and whether the board changed.
func Move2048(board [4][4]int, dir string) ([4][4]int, int, bool) {
	var out [4][4]int
	total := 0
	switch dir {
	case "left":
		for r := 0; r < 4; r++ {
			row, s := mergeRowLeft(board[r])
			out[r] = row
			total += s
		}
	case "right":
		for r := 0; r < 4; r++ {
			rev := reverseRow2048(board[r])
			merged, s := mergeRowLeft(rev)
			out[r] = reverseRow2048(merged)
			total += s
		}
	case "up":
		t := transpose2048(board)
		for r := 0; r < 4; r++ {
			row, s := mergeRowLeft(t[r])
			t[r] = row
			total += s
		}
		out = transpose2048(t)
	case "down":
		t := transpose2048(board)
		for r := 0; r < 4; r++ {
			rev := reverseRow2048(t[r])
			merged, s := mergeRowLeft(rev)
			t[r] = reverseRow2048(merged)
			total += s
		}
		out = transpose2048(t)
	default:
		return board, 0, false
	}
	return out, total, out != board
}

// mergeRowLeft slides and merges a single row leftward (standard 2048 rules).
func mergeRowLeft(row [4]int) ([4]int, int) {
	// compact: remove zeros
	tmp := [4]int{}
	ti := 0
	for _, v := range row {
		if v != 0 {
			tmp[ti] = v
			ti++
		}
	}
	// merge adjacent equals (left to right, each tile can only merge once)
	score := 0
	for i := 0; i < 3; i++ {
		if tmp[i] != 0 && tmp[i] == tmp[i+1] {
			tmp[i] *= 2
			score += tmp[i]
			tmp[i+1] = 0
			i++ // skip merged tile
		}
	}
	// compact again
	out := [4]int{}
	oi := 0
	for _, v := range tmp {
		if v != 0 {
			out[oi] = v
			oi++
		}
	}
	return out, score
}

func reverseRow2048(r [4]int) [4]int { return [4]int{r[3], r[2], r[1], r[0]} }

func transpose2048(b [4][4]int) [4][4]int {
	var t [4][4]int
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			t[c][r] = b[r][c]
		}
	}
	return t
}

// SpawnTile adds a new tile (90 % chance 2, 10 % chance 4) to a random empty cell.
func SpawnTile(board [4][4]int) [4][4]int {
	var empty [][2]int
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if board[r][c] == 0 {
				empty = append(empty, [2]int{r, c})
			}
		}
	}
	if len(empty) == 0 {
		return board
	}
	pos := empty[rand.Intn(len(empty))]
	val := 2
	if rand.Float64() < 0.1 {
		val = 4
	}
	board[pos[0]][pos[1]] = val
	return board
}

// NewBoard2048 returns a fresh board with two starting tiles.
func NewBoard2048() [4][4]int {
	var b [4][4]int
	b = SpawnTile(b)
	b = SpawnTile(b)
	return b
}

// Is2048Won returns true when any tile reaches 2048.
func Is2048Won(board [4][4]int) bool {
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if board[r][c] >= 2048 {
				return true
			}
		}
	}
	return false
}

// Is2048Over returns true when no move can change the board.
func Is2048Over(board [4][4]int) bool {
	for r := 0; r < 4; r++ {
		for c := 0; c < 4; c++ {
			if board[r][c] == 0 {
				return false
			}
			if c < 3 && board[r][c] == board[r][c+1] {
				return false
			}
			if r < 3 && board[r][c] == board[r+1][c] {
				return false
			}
		}
	}
	return true
}
