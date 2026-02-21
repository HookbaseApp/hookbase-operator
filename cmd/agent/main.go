// hookbase-agent is a lightweight Go tunnel agent that connects to the Hookbase
// tunnel server and forwards HTTP requests to a local service.
//
// Environment variables:
//   - HOOKBASE_WS_URL: WebSocket URL (default: wss://api.hookbase.app/tunnel/ws)
//   - HOOKBASE_TARGET_HOST: Target hostname (default: localhost)
//   - HOOKBASE_TARGET_PORT: Target port (required)
//   - HOOKBASE_TUNNEL_ID: Tunnel ID (required)
//   - HOOKBASE_AUTH_TOKEN: Auth token (required)
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hookbase/hookbase-operator/internal/agent"
)

func main() {
	cfg := agent.ConfigFromEnv()

	a, err := agent.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hookbase-agent: %v\n", err)
		os.Exit(1)
	}

	if err := a.Run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "hookbase-agent: %v\n", err)
		os.Exit(1)
	}
}
