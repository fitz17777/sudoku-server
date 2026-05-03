package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sudoku/internal/game"
	redisstore "github.com/user/sudoku/internal/redis"
	"github.com/user/sudoku/internal/templates"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Origin validation is handled at the HTTP layer via the session cookie check.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub is the central WebSocket message dispatcher.
type Hub struct {
	rooms      map[string]*Room
	register   chan *Client
	unregister chan *Client
	inbound    chan InboundMsg
	store      *redisstore.Client
	tmpl       *templates.Renderer
}

// New creates a Hub ready to be started with Run().
func New(store *redisstore.Client, tmpl *templates.Renderer) *Hub {
	return &Hub{
		rooms:      make(map[string]*Room),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		inbound:    make(chan InboundMsg, 256),
		store:      store,
		tmpl:       tmpl,
	}
}

// Run is the main event loop. Must be run in a goroutine.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return

		case c := <-h.register:
			room := h.getOrCreateRoom(c.roomID)
			room.addClient(c)
			log.Printf("hub: %s joined room %s", c.user.Username, c.roomID)
			// Auto-dispatch join_room so Redis presence and player-status broadcast
			// happen for every WS connection — host and joiner alike.
			room.dispatch(ctx, InboundMsg{
				Type:   "join_room",
				RoomID: c.roomID,
				UserID: c.user.UserID,
			})

		case c := <-h.unregister:
			room, ok := h.rooms[c.roomID]
			if !ok {
				close(c.send)
				continue
			}
			room.removeClient(c)
			close(c.send)
			if room.isEmpty() {
				delete(h.rooms, c.roomID)
				log.Printf("hub: room %s removed (empty)", c.roomID)
			} else {
				// Notify remaining player about the disconnect
				fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				redisRoom, err := h.store.GetRoom(fetchCtx, c.roomID)
				cancel()
				if err == nil {
					html := room.renderPlayerStatus(redisRoom)
					room.broadcast(buildWSMsg("player_left", html))
				}
			}

		case msg := <-h.inbound:
			room, ok := h.rooms[msg.RoomID]
			if !ok {
				continue
			}
			room.dispatch(ctx, msg)
		}
	}
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client with the hub.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, user game.User, roomID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	client := &Client{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, 256),
		user:   user,
		roomID: roomID,
	}

	h.register <- client
	go client.writePump()
	go client.readPump()
}

// GenerateRoomCode creates a unique 6-character uppercase hex room code.
func GenerateRoomCode() string {
	b := make([]byte, 3)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

// EnsureRoom creates or retrieves a room in Redis.
func (h *Hub) EnsureRoom(ctx context.Context, code string, hostUser game.User, mode game.GameMode) (*game.Room, error) {
	existing, err := h.store.GetRoom(ctx, code)
	if err == nil {
		return existing, nil
	}
	room := game.Room{
		Code:       code,
		Players:    []string{hostUser.UserID},
		Status:     game.StatusWaiting,
		Mode:       mode,
		HostUserID: hostUser.UserID,
		GameIDs:    []string{},
	}
	if err := h.store.StoreRoom(ctx, room); err != nil {
		return nil, err
	}
	return &room, nil
}

func (h *Hub) getOrCreateRoom(code string) *Room {
	if room, ok := h.rooms[code]; ok {
		return room
	}
	room := newRoom(code, h.store, h.tmpl)
	h.rooms[code] = room
	return room
}
