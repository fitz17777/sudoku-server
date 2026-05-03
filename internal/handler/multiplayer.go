package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/user/sudoku/internal/game"
	"github.com/user/sudoku/internal/hub"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
)


// MultiHandler handles multiplayer room routes.
type MultiHandler struct {
	store *redisstore.Client
	tmpl  *templates.Renderer
	hub   *hub.Hub
}

func NewMultiHandler(store *redisstore.Client, tmpl *templates.Renderer, h *hub.Hub) *MultiHandler {
	return &MultiHandler{store: store, tmpl: tmpl, hub: h}
}

// CreateRoom creates a new room with defaults and redirects straight to the lobby.
// Both GET and POST map here so the nav "Create Lobby" button (POST form) and any
// direct link (GET) both work identically.
func (h *MultiHandler) CreateRoom(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	code := hub.GenerateRoomCode()
	_, err := h.hub.EnsureRoom(r.Context(), code, *user, game.ModeCollaborative)
	if err != nil {
		http.Error(w, "could not create room", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/play/multi/room/"+code, http.StatusFound)
}

// JoinRoomPage shows the join-room form.
func (h *MultiHandler) JoinRoomPage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	renderPage(w, h.tmpl, "pages/room_join.html", map[string]interface{}{
		"User": user,
	})
}

// JoinRoom handles the POST to join an existing room.
func (h *MultiHandler) JoinRoom(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(r.FormValue("code")))
	if len(code) != 6 {
		renderPage(w, h.tmpl, "pages/room_join.html", map[string]interface{}{
			"User":  mustUser(r),
			"Error": "Invalid room code. Codes are 6 characters.",
		})
		return
	}

	_, err := h.store.GetRoom(r.Context(), code)
	if err != nil {
		renderPage(w, h.tmpl, "pages/room_join.html", map[string]interface{}{
			"User":  mustUser(r),
			"Error": "Room not found or expired.",
		})
		return
	}

	http.Redirect(w, r, "/play/multi/room/"+code, http.StatusFound)
}

// RoomPage shows the lobby or game page for a room.
func (h *MultiHandler) RoomPage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	code := chi.URLParam(r, "code")

	room, err := h.store.GetRoom(r.Context(), code)
	if err != nil {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	h.store.RefreshRoom(r.Context(), code)

	// If arriving after a completed game, reset to waiting so a new game can start.
	if room.Status == game.StatusComplete {
		room.Status = game.StatusWaiting
		room.GameIDs = nil
		h.store.StoreRoom(r.Context(), *room)
	}

	renderPage(w, h.tmpl, "pages/room_lobby.html", map[string]interface{}{
		"User":         user,
		"Room":         room,
		"Difficulties": game.Difficulties,
		"IsHost":       room.HostUserID == user.UserID,
	})
}
