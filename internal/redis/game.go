package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

const gameTTL = 6 * time.Hour

func gameKey(gameID string) string {
	return "game:" + gameID
}

// StoreGame saves a game state with a 6-hour TTL.
func (c *Client) StoreGame(ctx context.Context, gs game.GameState) error {
	b, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, gameKey(gs.GameID), b, gameTTL).Err()
}

// GetGame retrieves a game state by ID.
func (c *Client) GetGame(ctx context.Context, gameID string) (*game.GameState, error) {
	b, err := c.rdb.Get(ctx, gameKey(gameID)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("game %s not found", gameID)
	}
	if err != nil {
		return nil, err
	}
	var gs game.GameState
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// DeleteGame removes a game state from Redis.
func (c *Client) DeleteGame(ctx context.Context, gameID string) error {
	return c.rdb.Del(ctx, gameKey(gameID)).Err()
}
