package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hookbase/hookbase-operator/internal/agent"
)

func TestForwarderForward(t *testing.T) {
	// Create a mock local server
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "test-value")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"method": r.Method,
			"path":   r.URL.Path,
		})
	}))
	defer localServer.Close()

	// Parse host:port from test server
	// localServer.URL is like http://127.0.0.1:PORT
	host := "127.0.0.1"
	port := localServer.URL[len("http://127.0.0.1:"):]

	forwarder := agent.NewForwarder(host, port, nil)

	body := ""
	req := &agent.TunnelRequest{
		ID:      "test-123",
		Method:  "POST",
		URL:     "/api/webhooks?source=test",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    &body,
	}

	resp := forwarder.Forward(req)

	if resp.ID != "test-123" {
		t.Errorf("expected response ID test-123, got %s", resp.ID)
	}
	if resp.Status != 200 {
		t.Errorf("expected status 200, got %d", resp.Status)
	}
	if resp.Headers["X-Custom"] != "test-value" {
		t.Errorf("expected X-Custom header, got %v", resp.Headers)
	}

	var respBody map[string]string
	if err := json.Unmarshal([]byte(resp.Body), &respBody); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if respBody["method"] != "POST" {
		t.Errorf("expected method POST, got %s", respBody["method"])
	}
	if respBody["path"] != "/api/webhooks" {
		t.Errorf("expected path /api/webhooks, got %s", respBody["path"])
	}
}

func TestForwarderConnectionRefused(t *testing.T) {
	// Forwarder to a port with nothing listening
	forwarder := agent.NewForwarder("127.0.0.1", "19999", nil)

	req := &agent.TunnelRequest{
		ID:     "error-test",
		Method: "GET",
		URL:    "/health",
	}

	resp := forwarder.Forward(req)

	if resp.Status != 502 {
		t.Errorf("expected status 502 for connection refused, got %d", resp.Status)
	}
}

func TestForwarderHopByHopHeaderFiltering(t *testing.T) {
	var receivedHeaders http.Header

	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer localServer.Close()

	host := "127.0.0.1"
	port := localServer.URL[len("http://127.0.0.1:"):]

	forwarder := agent.NewForwarder(host, port, nil)

	req := &agent.TunnelRequest{
		ID:     "hop-test",
		Method: "GET",
		URL:    "/test",
		Headers: map[string]string{
			"Host":            "should-be-removed",
			"Connection":      "keep-alive",
			"X-Custom-Header": "should-be-kept",
			"Authorization":   "Bearer token",
		},
	}

	forwarder.Forward(req)

	if receivedHeaders.Get("Connection") != "" {
		t.Error("Connection header should be filtered")
	}
	if receivedHeaders.Get("X-Custom-Header") != "should-be-kept" {
		t.Error("X-Custom-Header should be forwarded")
	}
	if receivedHeaders.Get("Authorization") != "Bearer token" {
		t.Error("Authorization should be forwarded")
	}
}
