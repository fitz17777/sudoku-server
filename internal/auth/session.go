package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/user/sudoku/internal/game"
)

// SessionStore is the Redis operations needed by auth.
type SessionStore interface {
	StoreSession(ctx context.Context, token string, user game.User) error
	GetSession(ctx context.Context, token string) (*game.User, error)
	DeleteSession(ctx context.Context, token string) error
	StoreOIDCState(ctx context.Context, state string) error
	ValidateAndDeleteOIDCState(ctx context.Context, state string) (bool, error)
}

// GenerateToken creates a cryptographically random 32-byte hex token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CreateSession generates a token, stores the user, and returns the token.
func CreateSession(ctx context.Context, store SessionStore, claims *Claims) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	user := game.User{
		UserID:      claims.Sub,
		Username:    claims.PreferredUsername,
		DisplayName: claims.Name,
	}
	if err := store.StoreSession(ctx, token, user); err != nil {
		return "", fmt.Errorf("storing session: %w", err)
	}
	return token, nil
}
