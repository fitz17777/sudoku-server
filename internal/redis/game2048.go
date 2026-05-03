package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

const ttl2048 = 6 * time.Hour

// --- Solo 2048 state ---

func key2048(gameID string) string      { return "g2048:" + gameID }
func key2048Multi(gameID string) string { return "g2048m:" + gameID }

func (c *Client) Store2048State(ctx context.Context, gs game.Game2048State) error {
	b, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key2048(gs.GameID), b, ttl2048).Err()
}

func (c *Client) Get2048State(ctx context.Context, gameID string) (*game.Game2048State, error) {
	b, err := c.rdb.Get(ctx, key2048(gameID)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("2048 game %s not found", gameID)
	}
	if err != nil {
		return nil, err
	}
	var gs game.Game2048State
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// --- Multiplayer 2048 state ---

func (c *Client) Store2048MultiState(ctx context.Context, gs game.Game2048MultiState) error {
	b, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key2048Multi(gs.GameID), b, ttl2048).Err()
}

func (c *Client) Get2048MultiState(ctx context.Context, gameID string) (*game.Game2048MultiState, error) {
	b, err := c.rdb.Get(ctx, key2048Multi(gameID)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("2048 multi game %s not found", gameID)
	}
	if err != nil {
		return nil, err
	}
	var gs game.Game2048MultiState
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// --- Solo high-score leaderboard ---

const lb2048Key = "lb:2048"
const lb2048MetaKey = "lb2048_meta"

// Record2048HighScore updates the player's best score if higher than current.
func (c *Client) Record2048HighScore(ctx context.Context, userID, displayName string, score int) error {
	existing, err := c.rdb.ZScore(ctx, lb2048Key, userID).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if err == redis.Nil || float64(score) > existing {
		pipe := c.rdb.Pipeline()
		pipe.ZAdd(ctx, lb2048Key, redis.Z{Score: float64(score), Member: userID})
		pipe.HSet(ctx, lb2048MetaKey, userID, displayName)
		_, err = pipe.Exec(ctx)
	}
	return err
}

// Get2048PersonalBest returns the player's all-time best score (0 if none).
func (c *Client) Get2048PersonalBest(ctx context.Context, userID string) (int, error) {
	score, err := c.rdb.ZScore(ctx, lb2048Key, userID).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(score), nil
}

// Get2048Leaderboard returns the top N scores.
func (c *Client) Get2048Leaderboard(ctx context.Context, topN int) ([]game.Game2048LeaderboardEntry, error) {
	results, err := c.rdb.ZRevRangeWithScores(ctx, lb2048Key, 0, int64(topN-1)).Result()
	if err != nil {
		return nil, err
	}
	entries := make([]game.Game2048LeaderboardEntry, 0, len(results))
	for i, z := range results {
		uid := fmt.Sprintf("%v", z.Member)
		name := uid
		if n, err := c.rdb.HGet(ctx, lb2048MetaKey, uid).Result(); err == nil {
			name = n
		}
		entries = append(entries, game.Game2048LeaderboardEntry{
			Rank:        i + 1,
			DisplayName: name,
			BestScore:   int(z.Score),
		})
	}
	return entries, nil
}
