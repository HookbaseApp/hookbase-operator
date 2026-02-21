package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	clientPingInterval   = 30 * time.Second
	reconnectBaseDelay   = 1 * time.Second
	reconnectMaxDelay    = 60 * time.Second
	maxReconnectAttempts = 10
)

// RequestHandler is called for each incoming tunnel request.
type RequestHandler func(req *TunnelRequest)

// WSClient manages the WebSocket connection to the Hookbase tunnel server.
type WSClient struct {
	url     string
	handler RequestHandler
	logger  *slog.Logger

	mu   sync.Mutex
	conn *websocket.Conn

	reconnectAttempts int
	isClosing         bool
}

// NewWSClient creates a new WebSocket client.
func NewWSClient(url string, handler RequestHandler, logger *slog.Logger) *WSClient {
	return &WSClient{
		url:     url,
		handler: handler,
		logger:  logger,
	}
}

// Connect establishes the WebSocket connection and starts the read loop.
func (c *WSClient) Connect(ctx context.Context) error {
	if err := c.dial(ctx); err != nil {
		return err
	}
	c.reconnectAttempts = 0

	// Start background goroutines
	go c.readLoop(ctx)
	go c.clientPingLoop(ctx)

	return nil
}

// Close cleanly shuts down the WebSocket connection.
func (c *WSClient) Close() {
	c.mu.Lock()
	c.isClosing = true
	conn := c.conn
	c.mu.Unlock()

	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "agent shutting down")
	}
}

// SendResponse sends a tunnel response over the WebSocket.
func (c *WSClient) SendResponse(resp *TunnelResponse) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		c.logger.Warn("cannot send response, not connected", "requestId", resp.ID)
		return
	}

	data, err := json.Marshal(resp)
	if err != nil {
		c.logger.Error("failed to marshal response", "error", err, "requestId", resp.ID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.logger.Error("failed to send response", "error", err, "requestId", resp.ID)
	}
}

// dial establishes a new WebSocket connection.
func (c *WSClient) dial(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, c.url, &websocket.DialOptions{
		Subprotocols: []string{"hookbase-tunnel"},
	})
	if err != nil {
		return fmt.Errorf("WebSocket dial failed: %w", err)
	}

	// Set read limit to 10MB for large payloads
	conn.SetReadLimit(10 * 1024 * 1024)

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	c.logger.Info("connected to tunnel server")
	return nil
}

// readLoop reads messages from the WebSocket and dispatches them.
func (c *WSClient) readLoop(ctx context.Context) {
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			c.mu.Lock()
			isClosing := c.isClosing
			c.mu.Unlock()

			if isClosing {
				return
			}

			c.logger.Warn("WebSocket read error, attempting reconnect", "error", err)
			c.scheduleReconnect(ctx)
			return
		}

		var msg TunnelMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			c.logger.Error("failed to parse WebSocket message", "error", err)
			continue
		}

		switch {
		case msg.Type == "ping":
			// Server-initiated ping → respond with pong
			c.sendJSON(map[string]string{"type": "pong"})
		case msg.Type == "pong":
			// Response to our client ping, nothing to do
		case msg.ID != "" && msg.Method != "":
			// HTTP request to forward
			req := &TunnelRequest{
				ID:      msg.ID,
				Method:  msg.Method,
				URL:     msg.URL,
				Headers: msg.Headers,
				Body:    msg.Body,
			}
			c.handler(req)
		default:
			c.logger.Debug("unknown message type", "message", string(data))
		}
	}
}

// clientPingLoop sends periodic pings to the server.
func (c *WSClient) clientPingLoop(ctx context.Context) {
	ticker := time.NewTicker(clientPingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sendJSON(map[string]string{"type": "ping"})
		}
	}
}

// sendJSON sends a JSON message over the WebSocket.
func (c *WSClient) sendJSON(v interface{}) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return
	}

	data, err := json.Marshal(v)
	if err != nil {
		c.logger.Error("failed to marshal message", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.logger.Error("failed to send message", "error", err)
	}
}

// scheduleReconnect attempts to reconnect with exponential backoff.
func (c *WSClient) scheduleReconnect(ctx context.Context) {
	c.mu.Lock()
	if c.isClosing {
		c.mu.Unlock()
		return
	}
	c.reconnectAttempts++
	attempts := c.reconnectAttempts
	c.conn = nil
	c.mu.Unlock()

	if attempts > maxReconnectAttempts {
		c.logger.Error("max reconnect attempts reached, giving up",
			"attempts", attempts)
		return
	}

	// Exponential backoff with jitter
	base := math.Min(float64(reconnectBaseDelay)*math.Pow(2, float64(attempts-1)), float64(reconnectMaxDelay))
	jitter := rand.Float64() * float64(reconnectBaseDelay)
	delay := time.Duration(base + jitter)

	c.logger.Info("reconnecting",
		"attempt", attempts,
		"maxAttempts", maxReconnectAttempts,
		"delay", delay,
	)

	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}

	if err := c.dial(ctx); err != nil {
		c.logger.Error("reconnect failed", "error", err, "attempt", attempts)
		c.scheduleReconnect(ctx)
		return
	}

	c.mu.Lock()
	c.reconnectAttempts = 0
	c.mu.Unlock()

	c.logger.Info("reconnected successfully")
	go c.readLoop(ctx)
}
