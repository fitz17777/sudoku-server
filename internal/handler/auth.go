package handler

import (
	"net/http"
	"time"

	"github.com/user/sudoku/internal/auth"
	"github.com/user/sudoku/internal/templates"
)

// AuthHandler handles login, OIDC callback, and logout.
type AuthHandler struct {
	provider *auth.Provider
	store    auth.SessionStore
	tmpl     *templates.Renderer
	baseURL  string
}

func NewAuthHandler(provider *auth.Provider, store auth.SessionStore, tmpl *templates.Renderer, baseURL string) *AuthHandler {
	return &AuthHandler{provider: provider, store: store, tmpl: tmpl, baseURL: baseURL}
}

// Login initiates the OIDC authorization code flow.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	state, err := auth.GenerateToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.store.StoreOIDCState(r.Context(), state); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.provider.AuthCodeURL(state), http.StatusFound)
}

// Callback handles the OIDC redirect back from Keycloak.
func (h *AuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}

	valid, err := h.store.ValidateAndDeleteOIDCState(r.Context(), state)
	if err != nil || !valid {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	claims, err := h.provider.Exchange(r.Context(), code)
	if err != nil {
		http.Error(w, "authentication failed", http.StatusUnauthorized)
		return
	}

	token, err := auth.CreateSession(r.Context(), h.store, claims)
	if err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // required — Traefik terminates TLS, browser always connects over HTTPS
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})

	http.Redirect(w, r, "/", http.StatusFound)
}

// Logout clears the session and redirects to Keycloak's end_session_endpoint.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session"); err == nil {
		h.store.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	logoutURL := h.provider.EndSessionURL(h.baseURL)
	http.Redirect(w, r, logoutURL, http.StatusFound)
}
