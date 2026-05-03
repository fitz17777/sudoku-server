package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

const sessionTTL = 24 * time.Hour

func sessionKey(token string) string {
	return "session:" + token
}

// StoreSession saves a user session with a 24-hour sliding TTL.
func (c *Client) StoreSession(ctx context.Context, token string, user game.User) error {
	b, err := json.Marshal(user)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, sessionKey(token), b, sessionTTL).Err()
}

// GetSession retrieves a session and refreshes its TTL.
func (c *Client) GetSession(ctx context.Context, token string) (*game.User, error) {
	b, err := c.rdb.Get(ctx, sessionKey(token)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var u game.User
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, err
	}
	// Refresh sliding TTL
	c.rdb.Expire(ctx, sessionKey(token), sessionTTL)
	return &u, nil
}

// DeleteSession removes a session (logout).
func (c *Client) DeleteSession(ctx context.Context, token string) error {
	return c.rdb.Del(ctx, sessionKey(token)).Err()
}

// StoreOIDCState stores a random OIDC state string for CSRF protection (10 min TTL).
func (c *Client) StoreOIDCState(ctx context.Context, state string) error {
	return c.rdb.Set(ctx, "oidc_state:"+state, "1", 10*time.Minute).Err()
}

// ValidateAndDeleteOIDCState checks and removes an OIDC state token.
func (c *Client) ValidateAndDeleteOIDCState(ctx context.Context, state string) (bool, error) {
	key := "oidc_state:" + state
	val, err := c.rdb.GetDel(ctx, key).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redis state check: %w", err)
	}
	return val == "1", nil
}
