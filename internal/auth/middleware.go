package auth

import (
	"context"
	"net/http"
)

type contextKey string

const ctxKeyUser contextKey = "user"

// Middleware returns an HTTP middleware that requires a valid session cookie.
// Unauthenticated requests are redirected to /login.
func Middleware(store SessionStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value == "" {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			user, err := store.GetSession(r.Context(), cookie.Value)
			if err != nil || user == nil {
				http.SetCookie(w, &http.Cookie{
					Name:   "session",
					Value:  "",
					MaxAge: -1,
					Path:   "/",
				})
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKeyUser, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetUser retrieves the *game.User from context, returning nil if absent.
func GetUser(ctx context.Context) interface{} {
	return ctx.Value(ctxKeyUser)
}
