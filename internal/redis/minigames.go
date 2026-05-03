package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

const miniGameTTL = 6 * time.Hour

// --- TicTacToe ---

func tttKey(gameID string) string { return "ttt:" + gameID }

func (c *Client) StoreTicTacToeState(ctx context.Context, gs *game.TicTacToeState) error {
	b, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, tttKey(gs.GameID), b, miniGameTTL).Err()
}

func (c *Client) GetTicTacToeState(ctx context.Context, gameID string) (*game.TicTacToeState, error) {
	b, err := c.rdb.Get(ctx, tttKey(gameID)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("ttt game %s not found", gameID)
	}
	if err != nil {
		return nil, err
	}
	var gs game.TicTacToeState
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// --- Connect Four ---

func c4Key(gameID string) string { return "c4:" + gameID }

func (c *Client) StoreConnectFourState(ctx context.Context, gs *game.ConnectFourState) error {
	b, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, c4Key(gs.GameID), b, miniGameTTL).Err()
}

func (c *Client) GetConnectFourState(ctx context.Context, gameID string) (*game.ConnectFourState, error) {
	b, err := c.rdb.Get(ctx, c4Key(gameID)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("c4 game %s not found", gameID)
	}
	if err != nil {
		return nil, err
	}
	var gs game.ConnectFourState
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}

// --- Checkers ---

func checkersKey(gameID string) string { return "checkers:" + gameID }

func (c *Client) StoreCheckersState(ctx context.Context, gs *game.CheckersState) error {
	b, err := json.Marshal(gs)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, checkersKey(gs.GameID), b, miniGameTTL).Err()
}

func (c *Client) GetCheckersState(ctx context.Context, gameID string) (*game.CheckersState, error) {
	b, err := c.rdb.Get(ctx, checkersKey(gameID)).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("checkers game %s not found", gameID)
	}
	if err != nil {
		return nil, err
	}
	var gs game.CheckersState
	if err := json.Unmarshal(b, &gs); err != nil {
		return nil, err
	}
	return &gs, nil
}
