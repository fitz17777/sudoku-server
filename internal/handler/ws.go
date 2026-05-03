package handler

import (
	"net/http"

	"github.com/user/sudoku/internal/hub"
)

// WSHandler upgrades HTTP connections to WebSocket and hands them to the hub.
type WSHandler struct {
	hub *hub.Hub
}

func NewWSHandler(h *hub.Hub) *WSHandler {
	return &WSHandler{hub: h}
}

// ServeHTTP upgrades the connection and registers the client with the hub.
// The roomID comes from the query parameter "room".
func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if user.UserID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	roomID := r.URL.Query().Get("room")
	if roomID == "" {
		http.Error(w, "missing room parameter", http.StatusBadRequest)
		return
	}

	h.hub.ServeWS(w, r, *user, roomID)
}
