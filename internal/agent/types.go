package agent

// TunnelRequest represents an HTTP request forwarded through the tunnel.
type TunnelRequest struct {
	ID      string            `json:"id"`
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

// TunnelResponse represents an HTTP response sent back through the tunnel.
type TunnelResponse struct {
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// TunnelMessage is a generic message on the WebSocket.
type TunnelMessage struct {
	Type      string `json:"type,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`

	// Request fields
	ID      string            `json:"id,omitempty"`
	Method  string            `json:"method,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    *string           `json:"body,omitempty"`

	// Response fields
	Status int `json:"status,omitempty"`
}
