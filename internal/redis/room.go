package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

const roomTTL = 2 * time.Hour

func roomKey(code string) string {
	return "room:" + code
}

// StoreRoom saves a room with a 2-hour TTL.
func (c *Client) StoreRoom(ctx context.Context, room game.Room) error {
	b, err := json.Marshal(room)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, roomKey(room.Code), b, roomTTL).Err()
}

// GetRoom retrieves a room by its code.
func (c *Client) GetRoom(ctx context.Context, code string) (*game.Room, error) {
	b, err := c.rdb.Get(ctx, roomKey(code)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("room %s not found", code)
	}
	if err != nil {
		return nil, err
	}
	var room game.Room
	if err := json.Unmarshal(b, &room); err != nil {
		return nil, err
	}
	return &room, nil
}

// DeleteRoom removes a room.
func (c *Client) DeleteRoom(ctx context.Context, code string) error {
	return c.rdb.Del(ctx, roomKey(code)).Err()
}

// RefreshRoom resets the TTL on a room.
func (c *Client) RefreshRoom(ctx context.Context, code string) {
	c.rdb.Expire(ctx, roomKey(code), roomTTL)
}
