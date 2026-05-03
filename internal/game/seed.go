package game

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// PuzzleStore is the minimal interface the seeder needs from the Redis layer.
type PuzzleStore interface {
	PuzzleExists(ctx context.Context, difficulty Difficulty, number int) (bool, error)
	StorePuzzle(ctx context.Context, p Puzzle) error
}

// Seed generates all 125 puzzles (25 per difficulty) that are missing from Redis.
// It is idempotent: existing puzzles are never overwritten unless force is true.
func Seed(ctx context.Context, store PuzzleStore, force bool) error {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	total := 0
	generated := 0

	for _, diff := range Difficulties {
		for n := 1; n <= PuzzlesPerDifficulty; n++ {
			total++
			if !force {
				exists, err := store.PuzzleExists(ctx, diff, n)
				if err != nil {
					return fmt.Errorf("checking puzzle %s/%d: %w", diff, n, err)
				}
				if exists {
					continue
				}
			}

			p := GeneratePuzzle(diff, rng)
			p.Number = n

			if err := store.StorePuzzle(ctx, p); err != nil {
				return fmt.Errorf("storing puzzle %s/%d: %w", diff, n, err)
			}
			generated++
			log.Printf("seeder: generated %s puzzle %d/%d (clues: %d)",
				diff, n, PuzzlesPerDifficulty, p.ClueCount)
		}
	}

	log.Printf("seeder: done — %d/%d puzzles ready (%d generated)", total, total, generated)
	return nil
}
