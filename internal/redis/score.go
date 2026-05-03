package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/sudoku/internal/game"
)

// --- key helpers ---

func scoreKey(userID string, difficulty game.Difficulty, number int) string {
	return fmt.Sprintf("score:%s:%s:%02d", userID, difficulty, number)
}

func leaderboardKey(difficulty game.Difficulty, number int) string {
	return fmt.Sprintf("leaderboard:%s:%02d", difficulty, number)
}

// leaderboardMetaKey stores a hash of key → JSON LeaderboardMeta for display data.
func leaderboardMetaKey(difficulty game.Difficulty, number int) string {
	return fmt.Sprintf("leaderboard_meta:%s:%02d", difficulty, number)
}

// puzzleStatsKey stores a hash with "attempts" and "completions" fields.
func puzzleStatsKey(difficulty game.Difficulty, number int) string {
	return fmt.Sprintf("puzzle_stats:%s:%02d", difficulty, number)
}

// completionTimesKey stores a sorted set of elapsed_seconds → unique game key
// used to compute speed percentile.
func completionTimesKey(difficulty game.Difficulty, number int) string {
	return fmt.Sprintf("completion_times:%s:%02d", difficulty, number)
}

// --- personal scores ---

// UpsertScore stores/updates a player's best score for a puzzle.
func (c *Client) UpsertScore(ctx context.Context, userID string, difficulty game.Difficulty, number int, score int, elapsedSeconds int) error {
	key := scoreKey(userID, difficulty, number)
	existing, _ := c.GetScore(ctx, userID, difficulty, number)
	rec := game.ScoreRecord{
		BestScore:  score,
		BestTimeS:  elapsedSeconds,
		Attempts:   1,
		LastPlayed: time.Now(),
	}
	if existing != nil {
		rec.Attempts = existing.Attempts + 1
		if existing.BestScore > score {
			rec.BestScore = existing.BestScore
			rec.BestTimeS = existing.BestTimeS
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, key, b, 0).Err()
}

// GetScore retrieves a player's score record for a puzzle.
func (c *Client) GetScore(ctx context.Context, userID string, difficulty game.Difficulty, number int) (*game.ScoreRecord, error) {
	b, err := c.rdb.Get(ctx, scoreKey(userID, difficulty, number)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec game.ScoreRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

// --- leaderboard ---

// UpdateLeaderboard adds or improves a score entry.
// key     – unique identifier: userID for individuals, "collab_"+gameID for teams
// displayName – human-readable name shown on the board (may be "Alice & Bob")
// score / elapsedSeconds – for the sorted set and metadata
func (c *Client) UpdateLeaderboard(ctx context.Context, difficulty game.Difficulty, number int, key, displayName string, score, elapsedSeconds int) error {
	lbKey := leaderboardKey(difficulty, number)
	metaKey := leaderboardMetaKey(difficulty, number)

	// Only update if score improves (or entry is new)
	existingScore, err := c.rdb.ZScore(ctx, lbKey, key).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	if err == redis.Nil || float64(score) > existingScore {
		if err := c.rdb.ZAdd(ctx, lbKey, redis.Z{Score: float64(score), Member: key}).Err(); err != nil {
			return err
		}
		meta := game.LeaderboardMeta{DisplayName: displayName, ElapsedSeconds: elapsedSeconds}
		b, _ := json.Marshal(meta)
		c.rdb.HSet(ctx, metaKey, key, string(b))
	}
	return nil
}

// GetLeaderboard returns the top N entries (highest score first).
func (c *Client) GetLeaderboard(ctx context.Context, difficulty game.Difficulty, number int, topN int) ([]game.LeaderboardEntry, error) {
	lbKey := leaderboardKey(difficulty, number)
	metaKey := leaderboardMetaKey(difficulty, number)

	results, err := c.rdb.ZRevRangeWithScores(ctx, lbKey, 0, int64(topN-1)).Result()
	if err != nil {
		return nil, err
	}

	entries := make([]game.LeaderboardEntry, 0, len(results))
	for i, z := range results {
		key := fmt.Sprintf("%v", z.Member)

		displayName := key
		var bestTime int

		// Try metadata hash first (new format)
		if metaStr, err := c.rdb.HGet(ctx, metaKey, key).Result(); err == nil {
			var meta game.LeaderboardMeta
			if json.Unmarshal([]byte(metaStr), &meta) == nil {
				displayName = meta.DisplayName
				bestTime = meta.ElapsedSeconds
			}
		} else {
			// Legacy fallback: key was "userID:username"
			if rec, _ := c.GetScore(ctx, key, difficulty, number); rec != nil {
				bestTime = rec.BestTimeS
			}
		}

		entries = append(entries, game.LeaderboardEntry{
			Rank:        i + 1,
			UserID:      key,
			Username:    displayName, // keep Username field populated for compat
			DisplayName: displayName,
			Score:       int(z.Score),
			BestTimeS:   bestTime,
		})
	}
	return entries, nil
}

// GetPlayerRank returns the 1-based rank of key in the leaderboard, or 0 if absent.
func (c *Client) GetPlayerRank(ctx context.Context, difficulty game.Difficulty, number int, key string) (int, error) {
	rank, err := c.rdb.ZRevRank(ctx, leaderboardKey(difficulty, number), key).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return int(rank) + 1, nil
}

// --- puzzle attempt / completion stats ---

// IncrPuzzleAttempt increments the attempt counter for a puzzle.
func (c *Client) IncrPuzzleAttempt(ctx context.Context, difficulty game.Difficulty, number int) error {
	return c.rdb.HIncrBy(ctx, puzzleStatsKey(difficulty, number), "attempts", 1).Err()
}

// IncrPuzzleCompletion increments the completion counter for a puzzle.
func (c *Client) IncrPuzzleCompletion(ctx context.Context, difficulty game.Difficulty, number int) error {
	return c.rdb.HIncrBy(ctx, puzzleStatsKey(difficulty, number), "completions", 1).Err()
}

// GetPuzzleStats returns the attempt and completion counts for a puzzle.
func (c *Client) GetPuzzleStats(ctx context.Context, difficulty game.Difficulty, number int) (game.PuzzleStats, error) {
	vals, err := c.rdb.HMGet(ctx, puzzleStatsKey(difficulty, number), "attempts", "completions").Result()
	if err != nil {
		return game.PuzzleStats{}, err
	}
	parse := func(v interface{}) int64 {
		if v == nil {
			return 0
		}
		n, _ := strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64)
		return n
	}
	return game.PuzzleStats{Attempts: parse(vals[0]), Completions: parse(vals[1])}, nil
}

// --- speed percentile ---

// RecordCompletionTime adds a completion time entry to the sorted set.
// memberKey must be unique per completion (e.g. gameID).
func (c *Client) RecordCompletionTime(ctx context.Context, difficulty game.Difficulty, number int, memberKey string, elapsedSeconds int) error {
	return c.rdb.ZAdd(ctx, completionTimesKey(difficulty, number), redis.Z{
		Score:  float64(elapsedSeconds),
		Member: memberKey,
	}).Err()
}

// GetSpeedPercentile returns what percentage of recorded completions the given
// memberKey was faster than (0–100). Call after RecordCompletionTime.
func (c *Client) GetSpeedPercentile(ctx context.Context, difficulty game.Difficulty, number int, memberKey string) (int, error) {
	key := completionTimesKey(difficulty, number)
	// ZRank returns ascending rank (0 = fastest)
	rank, err := c.rdb.ZRank(ctx, key, memberKey).Result()
	if err != nil {
		return 0, err
	}
	total, err := c.rdb.ZCard(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	if total <= 1 {
		return 100, nil // sole completion
	}
	slower := total - rank - 1 // entries ranked below (higher time = slower)
	return int(float64(slower) / float64(total-1) * 100), nil
}

// --- difficulty-level leaderboard ---

func diffScoreKey(difficulty game.Difficulty) string {
	return fmt.Sprintf("diff_score:%s", difficulty)
}

func diffTimeKey(difficulty game.Difficulty) string {
	return fmt.Sprintf("diff_time:%s", difficulty)
}

func diffMetaKey(difficulty game.Difficulty) string {
	return fmt.Sprintf("diff_meta:%s", difficulty)
}

// UpdateDifficultyLeaderboard keeps each player's best single-game score for a difficulty.
func (c *Client) UpdateDifficultyLeaderboard(ctx context.Context, difficulty game.Difficulty, key, displayName string, score, elapsedSeconds int) error {
	// ZADD GT: only update if the new score is strictly higher than the stored one.
	if err := c.rdb.ZAddArgs(ctx, diffScoreKey(difficulty), redis.ZAddArgs{
		GT:      true,
		Members: []redis.Z{{Score: float64(score), Member: key}},
	}).Err(); err != nil {
		return err
	}
	// ZADD LT: add if new, update only if new time is lower (faster)
	if err := c.rdb.ZAddArgs(ctx, diffTimeKey(difficulty), redis.ZAddArgs{
		LT:      true,
		Members: []redis.Z{{Score: float64(elapsedSeconds), Member: key}},
	}).Err(); err != nil {
		return err
	}
	c.rdb.HSet(ctx, diffMetaKey(difficulty), key, displayName)
	return nil
}

// GetDifficultyLeaderboard returns the top N entries by best single-game score for a difficulty.
func (c *Client) GetDifficultyLeaderboard(ctx context.Context, difficulty game.Difficulty, topN int) ([]game.DifficultyLeaderboardEntry, error) {
	results, err := c.rdb.ZRevRangeWithScores(ctx, diffScoreKey(difficulty), 0, int64(topN-1)).Result()
	if err != nil {
		return nil, err
	}
	entries := make([]game.DifficultyLeaderboardEntry, 0, len(results))
	for i, z := range results {
		key := fmt.Sprintf("%v", z.Member)
		displayName := key
		if name, err := c.rdb.HGet(ctx, diffMetaKey(difficulty), key).Result(); err == nil {
			displayName = name
		}
		bestTime := 0
		if t, err := c.rdb.ZScore(ctx, diffTimeKey(difficulty), key).Result(); err == nil {
			bestTime = int(t)
		}
		entries = append(entries, game.DifficultyLeaderboardEntry{
			Rank:        i + 1,
			DisplayName: displayName,
			BestScore:   int(z.Score),
			BestTimeS:   bestTime,
		})
	}
	return entries, nil
}

// --- mini-game wins leaderboard ---

func winsKey(gameType string) string    { return "wins:" + gameType }
func winsMetaKey(gameType string) string { return "wins_meta:" + gameType }

// RecordWin increments the win counter for a player in a given game type.
func (c *Client) RecordWin(ctx context.Context, gameType, userID, displayName string) error {
	if err := c.rdb.ZIncrBy(ctx, winsKey(gameType), 1, userID).Err(); err != nil {
		return err
	}
	c.rdb.HSet(ctx, winsMetaKey(gameType), userID, displayName)
	return nil
}

// GetWinLeaderboard returns the top N players by win count for a game type.
func (c *Client) GetWinLeaderboard(ctx context.Context, gameType string, topN int) ([]game.WinLeaderboardEntry, error) {
	results, err := c.rdb.ZRevRangeWithScores(ctx, winsKey(gameType), 0, int64(topN-1)).Result()
	if err != nil {
		return nil, err
	}
	entries := make([]game.WinLeaderboardEntry, 0, len(results))
	for i, z := range results {
		key := fmt.Sprintf("%v", z.Member)
		displayName := key
		if name, err := c.rdb.HGet(ctx, winsMetaKey(gameType), key).Result(); err == nil {
			displayName = name
		}
		entries = append(entries, game.WinLeaderboardEntry{
			Rank:        i + 1,
			DisplayName: displayName,
			Wins:        int(z.Score),
		})
	}
	return entries, nil
}

// --- helpers ---

// FormatTime formats seconds into m:ss.
func FormatTime(seconds int) string {
	m := seconds / 60
	s := seconds % 60
	return fmt.Sprintf("%d:%02d", m, s)
}
