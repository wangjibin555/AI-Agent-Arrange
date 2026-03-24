package api

// Response represents the standard JSON envelope used by the HTTP API.
type Response struct {
	Success   bool           `json:"success"`
	Data      interface{}    `json:"data,omitempty"`
	Error     string         `json:"error,omitempty"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Timestamp string         `json:"timestamp,omitempty"`
}
