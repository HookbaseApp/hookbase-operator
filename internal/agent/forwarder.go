package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Hop-by-hop headers that should not be forwarded.
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailers":            true,
	"transfer-encoding":   true,
	"upgrade":             true,
	"host":                true,
}

// Forwarder forwards tunnel requests to a local HTTP service.
type Forwarder struct {
	targetHost string
	targetPort string
	client     *http.Client
	logger     *slog.Logger
}

// NewForwarder creates a new HTTP forwarder.
func NewForwarder(host, port string, logger *slog.Logger) *Forwarder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Forwarder{
		targetHost: host,
		targetPort: port,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // Don't follow redirects
			},
		},
		logger: logger,
	}
}

// Forward sends a tunnel request to the local service and returns the response.
func (f *Forwarder) Forward(req *TunnelRequest) *TunnelResponse {
	localURL := fmt.Sprintf("http://%s:%s%s", f.targetHost, f.targetPort, req.URL)

	// Build body reader
	var bodyReader io.Reader
	if req.Body != nil && *req.Body != "" {
		bodyReader = strings.NewReader(*req.Body)
	}

	// Create HTTP request
	httpReq, err := http.NewRequest(req.Method, localURL, bodyReader)
	if err != nil {
		f.logger.Error("failed to create request", "error", err, "requestId", req.ID)
		return f.errorResponse(req.ID, 502, "Bad Gateway", fmt.Sprintf("Failed to create request: %v", err))
	}

	// Forward headers, skipping hop-by-hop
	for key, value := range req.Headers {
		if !hopByHopHeaders[strings.ToLower(key)] {
			httpReq.Header.Set(key, value)
		}
	}

	// Execute request
	resp, err := f.client.Do(httpReq)
	if err != nil {
		f.logger.Error("failed to forward request", "error", err, "requestId", req.ID, "url", localURL)
		return f.errorResponse(req.ID, 502, "Bad Gateway",
			fmt.Sprintf("Could not connect to %s:%s: %v", f.targetHost, f.targetPort, err))
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		f.logger.Error("failed to read response body", "error", err, "requestId", req.ID)
		return f.errorResponse(req.ID, 502, "Bad Gateway", fmt.Sprintf("Failed to read response: %v", err))
	}

	// Collect response headers, skipping hop-by-hop
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if !hopByHopHeaders[strings.ToLower(key)] {
			headers[key] = strings.Join(values, ", ")
		}
	}

	f.logger.Debug("forwarded request",
		"requestId", req.ID,
		"method", req.Method,
		"url", req.URL,
		"status", resp.StatusCode,
	)

	return &TunnelResponse{
		ID:      req.ID,
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    string(body),
	}
}

// errorResponse creates an error TunnelResponse.
func (f *Forwarder) errorResponse(requestID string, status int, errTitle, errMessage string) *TunnelResponse {
	body, _ := json.Marshal(map[string]string{
		"error":   errTitle,
		"message": errMessage,
	})
	return &TunnelResponse{
		ID:      requestID,
		Status:  status,
		Headers: map[string]string{"content-type": "application/json"},
		Body:    string(body),
	}
}
