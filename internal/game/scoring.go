package game

import "math"

// BasePoints returns the base display points for a difficulty (used in lobby UI).
func BasePoints(d Difficulty) int {
	switch d {
	case Easy:
		return 1000
	case Medium:
		return 1200
	case Hard:
		return 1440
	case Expert:
		return 1728
	case Master:
		return 2074
	}
	return 1000
}

// ScoreForCell returns the points awarded for a single correctly entered cell.
//
// Time multiplier (elapsed since game start):
//
//	< 1 min  → 200 pts (2×)
//	< 5 min  → 150 pts (1.5×)
//	< 10 min → 125 pts (1.25×)
//	≥ 10 min → 100 pts (1×)
//
// Difficulty multiplier: +20 % per level above Easy (additive)
//
//	Easy=1.0×  Medium=1.2×  Hard=1.4×  Expert=1.6×  Master=1.8×
func ScoreForCell(d Difficulty, elapsedSeconds float64) int {
	var timeMult float64
	switch {
	case elapsedSeconds < 60:
		timeMult = 2.0
	case elapsedSeconds < 300:
		timeMult = 1.5
	case elapsedSeconds < 600:
		timeMult = 1.25
	default:
		timeMult = 1.0
	}

	diffLevel := map[Difficulty]int{
		Easy:   0,
		Medium: 1,
		Hard:   2,
		Expert: 3,
		Master: 4,
	}[d]
	diffMult := 1.0 + float64(diffLevel)*0.2

	return int(math.Round(100.0 * timeMult * diffMult))
}
