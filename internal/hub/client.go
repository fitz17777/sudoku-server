package hub

import (
	"encoding/json"
	"log"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sudoku/internal/game"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 90 * time.Second
	pingPeriod = 60 * time.Second
	maxMsgSize = 4096
)

// Client represents a single WebSocket connection.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	user   game.User
	roomID string
}

// readPump pumps messages from the WebSocket to the hub's inbound channel.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMsgSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		msg := parseMessage(message, c.user, c.roomID)
		if msg.Type == "ping" {
			// Handled by pong handler; ignore
			continue
		}
		c.hub.inbound <- msg
	}
}

// writePump pumps messages from the hub to the WebSocket.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("ws write error: %v", err)
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// parseMessage converts a WebSocket payload into an InboundMsg.
// HTMX ws-send serialises form inputs as JSON ({"type":"...","room_id":"...",...}).
// All values arrive as strings since they come from HTML input elements.
func parseMessage(data []byte, user game.User, roomID string) InboundMsg {
	// rawWS mirrors what HTMX ws-send sends: all fields are strings.
	var raw struct {
		Type       string `json:"type"`
		RoomID     string `json:"room_id"`
		Row        string `json:"row"`
		Col        string `json:"col"`
		Value      string `json:"value"`
		Number     string `json:"number"`
		Mode       string `json:"mode"`
		Difficulty string `json:"difficulty"`
		GameType   string `json:"game_type"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return InboundMsg{Type: "unknown"}
	}

	toInt := func(s string) int { v, _ := strconv.Atoi(s); return v }

	msg := InboundMsg{
		Type:       raw.Type,
		RoomID:     raw.RoomID,
		UserID:     user.UserID,
		Username:   user.Username,
		Row:        toInt(raw.Row),
		Col:        toInt(raw.Col),
		Value:      toInt(raw.Value),
		Number:     toInt(raw.Number),
		Mode:       raw.Mode,
		Difficulty: raw.Difficulty,
		GameType:   raw.GameType,
	}
	if msg.RoomID == "" {
		msg.RoomID = roomID
	}
	return msg
}
