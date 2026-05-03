package redisstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

func puzzleKey(difficulty game.Difficulty, number int) string {
	return fmt.Sprintf("puzzle:%s:%02d", difficulty, number)
}

// PuzzleExists reports whether a puzzle key is already stored.
func (c *Client) PuzzleExists(ctx context.Context, difficulty game.Difficulty, number int) (bool, error) {
	n, err := c.rdb.Exists(ctx, puzzleKey(difficulty, number)).Result()
	return n > 0, err
}

// StorePuzzle serialises and stores a puzzle (no TTL — permanent).
func (c *Client) StorePuzzle(ctx context.Context, p game.Puzzle) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, puzzleKey(p.Difficulty, p.Number), b, 0).Err()
}

// GetPuzzle retrieves a puzzle by difficulty and 1-based number.
func (c *Client) GetPuzzle(ctx context.Context, difficulty game.Difficulty, number int) (*game.Puzzle, error) {
	b, err := c.rdb.Get(ctx, puzzleKey(difficulty, number)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("puzzle %s/%d not found", difficulty, number)
	}
	if err != nil {
		return nil, err
	}
	var p game.Puzzle
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
