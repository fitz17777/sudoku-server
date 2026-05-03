package game

import (
	"math/rand"
	"time"
)

// GeneratePuzzle creates a new Sudoku puzzle at the given difficulty.
// It fills a complete valid grid, then removes clues until the target count
// is reached while preserving a unique solution.
func GeneratePuzzle(difficulty Difficulty, rng *rand.Rand) Puzzle {
	solution := fillGrid(rng)
	minClues, maxClues := DifficultyClueRange(difficulty)
	targetClues := minClues + rng.Intn(maxClues-minClues+1)

	grid := solution
	positions := shuffledPositions(rng)

	deadline := time.Now().Add(2 * time.Second)
	clues := 81

	for _, pos := range positions {
		if clues <= targetClues {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		r, c := pos[0], pos[1]
		if grid[r][c] == 0 {
			continue
		}
		saved := grid[r][c]
		grid[r][c] = 0

		if !hasUniqueSolution(grid) {
			grid[r][c] = saved
		} else {
			clues--
		}
	}

	return Puzzle{
		Difficulty: difficulty,
		Grid:       grid,
		Solution:   solution,
		ClueCount:  clues,
	}
}

// ValidatePlacement checks whether placing val at (row,col) is legal
// given the current grid state (ignoring the cell itself).
func ValidatePlacement(grid [9][9]int, row, col, val int) bool {
	if val < 1 || val > 9 {
		return false
	}
	// Check row
	for c := 0; c < 9; c++ {
		if c != col && grid[row][c] == val {
			return false
		}
	}
	// Check column
	for r := 0; r < 9; r++ {
		if r != row && grid[r][col] == val {
			return false
		}
	}
	// Check 3×3 box
	boxR, boxC := (row/3)*3, (col/3)*3
	for r := boxR; r < boxR+3; r++ {
		for c := boxC; c < boxC+3; c++ {
			if (r != row || c != col) && grid[r][c] == val {
				return false
			}
		}
	}
	return true
}

// IsBoardComplete returns true when all cells are filled and the solution matches.
func IsBoardComplete(grid, solution [9][9]int) bool {
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			if grid[r][c] != solution[r][c] {
				return false
			}
		}
	}
	return true
}

// --- internal helpers ---

// fillGrid produces a fully solved 9×9 Sudoku grid using backtracking.
func fillGrid(rng *rand.Rand) [9][9]int {
	var grid [9][9]int
	fillCell(&grid, 0, rng)
	return grid
}

func fillCell(grid *[9][9]int, pos int, rng *rand.Rand) bool {
	if pos == 81 {
		return true
	}
	r, c := pos/9, pos%9
	digits := rng.Perm(9) // 0–8, add 1 for actual digit
	for _, d := range digits {
		val := d + 1
		if isValid(grid, r, c, val) {
			grid[r][c] = val
			if fillCell(grid, pos+1, rng) {
				return true
			}
			grid[r][c] = 0
		}
	}
	return false
}

func isValid(grid *[9][9]int, row, col, val int) bool {
	for i := 0; i < 9; i++ {
		if grid[row][i] == val || grid[i][col] == val {
			return false
		}
	}
	boxR, boxC := (row/3)*3, (col/3)*3
	for r := boxR; r < boxR+3; r++ {
		for c := boxC; c < boxC+3; c++ {
			if grid[r][c] == val {
				return false
			}
		}
	}
	return true
}

// hasUniqueSolution returns true iff the puzzle has exactly one solution.
// It uses backtracking that stops as soon as a second solution is found.
func hasUniqueSolution(grid [9][9]int) bool {
	count := 0
	countSolutions(&grid, 0, &count)
	return count == 1
}

func countSolutions(grid *[9][9]int, pos int, count *int) {
	if *count > 1 {
		return // pruning: already found 2, no need to continue
	}
	if pos == 81 {
		*count++
		return
	}
	r, c := pos/9, pos%9
	if grid[r][c] != 0 {
		countSolutions(grid, pos+1, count)
		return
	}
	for val := 1; val <= 9; val++ {
		if isValid(grid, r, c, val) {
			grid[r][c] = val
			countSolutions(grid, pos+1, count)
			grid[r][c] = 0
			if *count > 1 {
				return
			}
		}
	}
}

func shuffledPositions(rng *rand.Rand) [][2]int {
	positions := make([][2]int, 81)
	for i := range positions {
		positions[i] = [2]int{i / 9, i % 9}
	}
	rng.Shuffle(len(positions), func(i, j int) {
		positions[i], positions[j] = positions[j], positions[i]
	})
	return positions
}
