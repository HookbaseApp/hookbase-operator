package test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hookbase/hookbase-operator/internal/agent"
	"nhooyr.io/websocket"
)

// mockTunnelServer creates a mock WebSocket server that implements the tunnel protocol.
type mockTunnelServer struct {
	server         *httptest.Server
	mu             sync.Mutex
	clientMessages []map[string]interface{}
	sendCh         chan []byte
	onConnect      func()
}

func newMockTunnelServer(t *testing.T) *mockTunnelServer {
	t.Helper()
	m := &mockTunnelServer{
		sendCh: make(chan []byte, 100),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{"hookbase-tunnel"},
		})
		if err != nil {
			t.Logf("accept error: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test done")

		if m.onConnect != nil {
			m.onConnect()
		}

		ctx := r.Context()

		// Read loop in goroutine
		go func() {
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var msg map[string]interface{}
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}
				m.mu.Lock()
				m.clientMessages = append(m.clientMessages, msg)
				m.mu.Unlock()

				// Auto-respond to pings with pong
				if msgType, ok := msg["type"].(string); ok && msgType == "ping" {
					pong, _ := json.Marshal(map[string]string{"type": "pong"})
					conn.Write(ctx, websocket.MessageText, pong)
				}
			}
		}()

		// Send loop
		for {
			select {
			case <-ctx.Done():
				return
			case data := <-m.sendCh:
				if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
					return
				}
			}
		}
	})

	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockTunnelServer) wsURL() string {
	return "ws" + strings.TrimPrefix(m.server.URL, "http") + "/tunnel/ws"
}

func (m *mockTunnelServer) close() {
	m.server.Close()
}

func (m *mockTunnelServer) sendMessage(msg interface{}) {
	data, _ := json.Marshal(msg)
	m.sendCh <- data
}

func (m *mockTunnelServer) getClientMessages() []map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := make([]map[string]interface{}, len(m.clientMessages))
	copy(msgs, m.clientMessages)
	return msgs
}

func TestWSClientConnect(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	connected := false
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	connected = true
	defer client.Close()

	if !connected {
		t.Error("expected to be connected")
	}
}

func TestWSClientServerPingPong(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	// Give readLoop time to start
	time.Sleep(100 * time.Millisecond)

	// Server sends ping
	srv.sendMessage(map[string]interface{}{
		"type":      "ping",
		"timestamp": time.Now().UnixMilli(),
	})

	// Wait for client to respond
	time.Sleep(500 * time.Millisecond)

	msgs := srv.getClientMessages()
	foundPong := false
	for _, msg := range msgs {
		if msgType, ok := msg["type"].(string); ok && msgType == "pong" {
			foundPong = true
			break
		}
	}

	if !foundPong {
		t.Errorf("expected client to send pong in response to server ping, got messages: %v", msgs)
	}
}

func TestWSClientRequestForwarding(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	receivedRequests := make(chan *agent.TunnelRequest, 10)
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {
		receivedRequests <- req
	}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	time.Sleep(100 * time.Millisecond)

	// Server sends a tunnel request
	body := `{"key": "value"}`
	srv.sendMessage(map[string]interface{}{
		"id":      "req-001",
		"method":  "POST",
		"url":     "/api/webhooks?source=github",
		"headers": map[string]string{"Content-Type": "application/json"},
		"body":    body,
	})

	// Wait for handler to receive it
	select {
	case req := <-receivedRequests:
		if req.ID != "req-001" {
			t.Errorf("expected ID req-001, got %s", req.ID)
		}
		if req.Method != "POST" {
			t.Errorf("expected method POST, got %s", req.Method)
		}
		if req.URL != "/api/webhooks?source=github" {
			t.Errorf("expected URL /api/webhooks?source=github, got %s", req.URL)
		}
		if req.Body == nil || *req.Body != body {
			t.Errorf("expected body %q, got %v", body, req.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for request to be forwarded to handler")
	}
}

func TestWSClientSendResponse(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	time.Sleep(100 * time.Millisecond)

	// Send a response through the client
	resp := &agent.TunnelResponse{
		ID:      "req-002",
		Status:  200,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    `{"result": "ok"}`,
	}
	client.SendResponse(resp)

	// Wait and check what server received
	time.Sleep(500 * time.Millisecond)

	msgs := srv.getClientMessages()
	var foundResp map[string]interface{}
	for _, msg := range msgs {
		if id, ok := msg["id"].(string); ok && id == "req-002" {
			foundResp = msg
			break
		}
	}

	if foundResp == nil {
		t.Fatalf("server did not receive response, got messages: %v", msgs)
	}
	if status, ok := foundResp["status"].(float64); !ok || int(status) != 200 {
		t.Errorf("expected status 200, got %v", foundResp["status"])
	}
	if body, ok := foundResp["body"].(string); !ok || body != `{"result": "ok"}` {
		t.Errorf("expected body, got %v", foundResp["body"])
	}
}

func TestWSClientMultiplePingResponses(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	time.Sleep(100 * time.Millisecond)

	// Send 3 pings
	for i := 0; i < 3; i++ {
		srv.sendMessage(map[string]interface{}{
			"type":      "ping",
			"timestamp": time.Now().UnixMilli(),
		})
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	msgs := srv.getClientMessages()
	pongCount := 0
	for _, msg := range msgs {
		if msgType, ok := msg["type"].(string); ok && msgType == "pong" {
			pongCount++
		}
	}

	if pongCount < 3 {
		t.Errorf("expected at least 3 pong responses, got %d (total msgs: %d)", pongCount, len(msgs))
	}
}

func TestWSClientIgnoresUnknownMessages(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	receivedRequests := make(chan *agent.TunnelRequest, 10)
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {
		receivedRequests <- req
	}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer client.Close()

	time.Sleep(100 * time.Millisecond)

	// Send unknown message type — should not crash
	srv.sendMessage(map[string]string{"type": "unknown_msg"})
	time.Sleep(100 * time.Millisecond)

	// Send a valid request after — should still work
	srv.sendMessage(map[string]interface{}{
		"id":     "req-003",
		"method": "GET",
		"url":    "/test",
	})

	select {
	case req := <-receivedRequests:
		if req.ID != "req-003" {
			t.Errorf("expected ID req-003, got %s", req.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out — client may have crashed on unknown message")
	}
}

func TestWSClientCloseGraceful(t *testing.T) {
	srv := newMockTunnelServer(t)
	defer srv.close()

	logger := slog.Default()
	var handler agent.RequestHandler = func(req *agent.TunnelRequest) {}

	client := agent.NewWSClient(srv.wsURL(), handler, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Close should not panic or block
	done := make(chan struct{})
	go func() {
		client.Close()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("Close() blocked for too long")
	}
}

func TestAgentConcurrencyLimiting(t *testing.T) {
	// Create a local server that takes 200ms to respond
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer localServer.Close()

	host := "127.0.0.1"
	port := localServer.URL[len("http://127.0.0.1:"):]

	// Create forwarder with small concurrency
	forwarder := agent.NewForwarder(host, port, slog.Default())

	// Fire 5 concurrent requests
	var wg sync.WaitGroup
	results := make(chan *agent.TunnelResponse, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := &agent.TunnelRequest{
				ID:     fmt.Sprintf("concurrent-%d", id),
				Method: "GET",
				URL:    "/test",
			}
			resp := forwarder.Forward(req)
			results <- resp
		}(i)
	}

	wg.Wait()
	close(results)

	successCount := 0
	for resp := range results {
		if resp.Status == 200 {
			successCount++
		}
	}

	if successCount != 5 {
		t.Errorf("expected 5 successful concurrent responses, got %d", successCount)
	}
}

func TestForwarderLargeBody(t *testing.T) {
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Return a 100KB body
		data := strings.Repeat("x", 100*1024)
		w.Write([]byte(data))
	}))
	defer localServer.Close()

	host := "127.0.0.1"
	port := localServer.URL[len("http://127.0.0.1:"):]
	forwarder := agent.NewForwarder(host, port, nil)

	req := &agent.TunnelRequest{
		ID:     "large-body-test",
		Method: "GET",
		URL:    "/large",
	}

	resp := forwarder.Forward(req)

	if resp.Status != 200 {
		t.Errorf("expected status 200, got %d", resp.Status)
	}
	if len(resp.Body) != 100*1024 {
		t.Errorf("expected body length %d, got %d", 100*1024, len(resp.Body))
	}
}

func TestForwarderPreservesQueryString(t *testing.T) {
	var receivedURL string
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	host := "127.0.0.1"
	port := localServer.URL[len("http://127.0.0.1:"):]
	forwarder := agent.NewForwarder(host, port, nil)

	req := &agent.TunnelRequest{
		ID:     "qs-test",
		Method: "GET",
		URL:    "/api/webhooks?source=stripe&event=payment.completed",
	}

	resp := forwarder.Forward(req)

	if resp.Status != 200 {
		t.Errorf("expected status 200, got %d", resp.Status)
	}
	if receivedURL != "/api/webhooks?source=stripe&event=payment.completed" {
		t.Errorf("query string not preserved, got %s", receivedURL)
	}
}

func TestForwarderNoRedirectFollowing(t *testing.T) {
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	host := "127.0.0.1"
	port := localServer.URL[len("http://127.0.0.1:"):]
	forwarder := agent.NewForwarder(host, port, nil)

	req := &agent.TunnelRequest{
		ID:     "redirect-test",
		Method: "GET",
		URL:    "/redirect",
	}

	resp := forwarder.Forward(req)

	// Should return the redirect response, not follow it
	if resp.Status != 307 {
		t.Errorf("expected status 307 (redirect not followed), got %d", resp.Status)
	}
}
