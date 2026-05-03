package redisstore

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Client wraps go-redis and provides all application storage operations.
type Client struct {
	rdb *redis.Client
}

// New creates and validates a Redis connection.
func New(addr, password string) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Close closes the underlying connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}
