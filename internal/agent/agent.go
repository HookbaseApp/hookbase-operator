// Package agent implements a Go tunnel agent that connects to the Hookbase
// tunnel server via WebSocket and forwards HTTP requests to a local service.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Config holds the agent configuration.
type Config struct {
	// WSUrl is the WebSocket URL to connect to.
	WSUrl string
	// TargetHost is the hostname to forward requests to (default: localhost).
	TargetHost string
	// TargetPort is the port to forward requests to.
	TargetPort string
	// TunnelID is the tunnel identifier.
	TunnelID string
	// AuthToken is the authentication token for the tunnel.
	AuthToken string
	// MaxConcurrent is the maximum number of concurrent request forwards.
	MaxConcurrent int
}

// ConfigFromEnv reads the agent config from environment variables.
func ConfigFromEnv() *Config {
	cfg := &Config{
		WSUrl:         os.Getenv("HOOKBASE_WS_URL"),
		TargetHost:    os.Getenv("HOOKBASE_TARGET_HOST"),
		TargetPort:    os.Getenv("HOOKBASE_TARGET_PORT"),
		TunnelID:      os.Getenv("HOOKBASE_TUNNEL_ID"),
		AuthToken:     os.Getenv("HOOKBASE_AUTH_TOKEN"),
		MaxConcurrent: 50,
	}
	if cfg.TargetHost == "" {
		cfg.TargetHost = "localhost"
	}
	if cfg.WSUrl == "" {
		cfg.WSUrl = "wss://api.hookbase.app/tunnel/ws"
	}
	return cfg
}

// Agent orchestrates the WebSocket client and HTTP forwarder.
type Agent struct {
	config    *Config
	ws        *WSClient
	forwarder *Forwarder
	logger    *slog.Logger
	sem       chan struct{} // concurrency limiter
	wg        sync.WaitGroup
}

// New creates a new Agent.
func New(cfg *Config) (*Agent, error) {
	if cfg.TargetPort == "" {
		return nil, fmt.Errorf("HOOKBASE_TARGET_PORT is required")
	}
	if cfg.TunnelID == "" {
		return nil, fmt.Errorf("HOOKBASE_TUNNEL_ID is required")
	}
	if cfg.AuthToken == "" {
		return nil, fmt.Errorf("HOOKBASE_AUTH_TOKEN is required")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	forwarder := NewForwarder(cfg.TargetHost, cfg.TargetPort, logger)

	a := &Agent{
		config:    cfg,
		forwarder: forwarder,
		logger:    logger,
		sem:       make(chan struct{}, cfg.MaxConcurrent),
	}

	// Build WebSocket URL with auth params
	wsURL := fmt.Sprintf("%s?token=%s&tunnelId=%s", cfg.WSUrl, cfg.AuthToken, cfg.TunnelID)
	a.ws = NewWSClient(wsURL, a.handleRequest, logger)

	return a, nil
}

// Run starts the agent and blocks until context is cancelled or SIGTERM/SIGINT.
func (a *Agent) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case sig := <-sigCh:
			a.logger.Info("received signal, shutting down", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	a.logger.Info("starting hookbase-agent",
		"tunnelId", a.config.TunnelID,
		"target", fmt.Sprintf("%s:%s", a.config.TargetHost, a.config.TargetPort),
	)

	// Connect and run
	if err := a.ws.Connect(ctx); err != nil {
		return fmt.Errorf("initial connection failed: %w", err)
	}

	// Block until context cancelled
	<-ctx.Done()

	a.logger.Info("shutting down, waiting for in-flight requests")
	a.wg.Wait()
	a.ws.Close()

	return nil
}

// handleRequest is called for each incoming tunnel request.
func (a *Agent) handleRequest(req *TunnelRequest) {
	// Acquire semaphore for concurrency limiting
	select {
	case a.sem <- struct{}{}:
	default:
		// At max concurrency, send 503
		a.logger.Warn("max concurrency reached, rejecting request", "requestId", req.ID)
		resp := &TunnelResponse{
			ID:      req.ID,
			Status:  503,
			Headers: map[string]string{"content-type": "application/json"},
			Body:    `{"error":"Service Unavailable","message":"Agent at max concurrency"}`,
		}
		a.ws.SendResponse(resp)
		return
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer func() { <-a.sem }()

		resp := a.forwarder.Forward(req)
		a.ws.SendResponse(resp)
	}()
}
