package api

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// ExecutionEvent is the unified event model for task and workflow executions.
type ExecutionEvent struct {
	Type                    string                 `json:"type"`                                 // event type: step_started, completed, failed, cancelled ...
	ExecutionID             string                 `json:"execution_id"`                         // unified execution ID
	ExecutionType           string                 `json:"execution_type"`                       // task | workflow
	Status                  string                 `json:"status"`                               // execution or step status
	RecoveryStatus          string                 `json:"recovery_status,omitempty"`            // recovery lifecycle state for workflow executions
	SupersededByExecutionID string                 `json:"superseded_by_execution_id,omitempty"` // resumed execution that superseded this execution
	Message                 string                 `json:"message"`                              // human-readable message
	Result                  map[string]interface{} `json:"result,omitempty"`                     // event result payload
	Error                   string                 `json:"error,omitempty"`                      // error message
	Data                    map[string]interface{} `json:"data,omitempty"`                       // extra event data
	Timestamp               time.Time              `json:"timestamp"`                            // event timestamp
}

// SSE连接对象
type EventChannel struct {
	ExecutionID string
	ClientID    string
	Channel     chan ExecutionEvent
	lastActive  atomic.Int64
}

func newEventChannel(executionID string, clientID string) *EventChannel {
	channel := &EventChannel{
		ExecutionID: executionID,
		ClientID:    clientID,
		Channel:     make(chan ExecutionEvent, 10), // buffered channel
	}
	channel.touch(time.Now())
	return channel
}

func (c *EventChannel) touch(now time.Time) {
	c.lastActive.Store(now.UnixNano())
}

func (c *EventChannel) lastActiveTime() time.Time {
	lastActive := c.lastActive.Load()
	if lastActive == 0 {
		return time.Time{}
	}
	return time.Unix(0, lastActive)
}

// EventStreamManager manages SSE connections and event distribution
type EventStreamManager struct {
	clients map[string][]*EventChannel // executionID -> list of client channels
	mu      sync.RWMutex
	metrics *monitor.SSEMetrics
}

// NewEventStreamManager creates a new event stream manager
func NewEventStreamManager() *EventStreamManager {
	manager := &EventStreamManager{
		clients: make(map[string][]*EventChannel),
	}

	// Start cleanup goroutine to remove inactive clients
	go manager.cleanupInactiveClients()

	return manager
}

func (m *EventStreamManager) SetMetrics(metrics *monitor.SSEMetrics) {
	m.metrics = metrics
}

// Subscribe subscribes a client to execution events.
func (m *EventStreamManager) Subscribe(executionID string, clientID string, executionType string) *EventChannel {
	m.mu.Lock()
	defer m.mu.Unlock()

	channel := newEventChannel(executionID, clientID)

	m.clients[executionID] = append(m.clients[executionID], channel)
	if m.metrics != nil {
		m.metrics.ObserveConnection(executionType)
	}

	logger.Info("Client subscribed to execution events",
		zap.String("execution_id", executionID),
		zap.String("client_id", clientID),
		zap.Int("total_clients", len(m.clients[executionID])),
	)

	return channel
}

// Unsubscribe unsubscribes a client from execution events.
func (m *EventStreamManager) Unsubscribe(executionID string, clientID string, executionType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	clients := m.clients[executionID]
	for i, client := range clients {
		if client.ClientID == clientID {
			close(client.Channel)
			m.clients[executionID] = append(clients[:i], clients[i+1:]...)
			if m.metrics != nil {
				m.metrics.ObserveDisconnect(executionType)
			}

			logger.Info("Client unsubscribed from execution events",
				zap.String("execution_id", executionID),
				zap.String("client_id", clientID),
				zap.Int("remaining_clients", len(m.clients[executionID])),
			)

			// Remove execution entry if no clients left
			if len(m.clients[executionID]) == 0 {
				delete(m.clients, executionID)
			}
			break
		}
	}
}

// Publish publishes an event to all subscribers of an execution.
func (m *EventStreamManager) Publish(event ExecutionEvent) {
	start := time.Now()
	now := start
	m.mu.RLock()
	defer m.mu.RUnlock()

	clients := m.clients[event.ExecutionID]

	if len(clients) == 0 {
		return // no subscribers
	}

	logger.Debug("Publishing event",
		zap.String("execution_id", event.ExecutionID),
		zap.String("type", event.Type),
		zap.Int("subscribers", len(clients)),
	)

	for _, client := range clients {
		select {
		case client.Channel <- event:
			client.touch(now)
			if m.metrics != nil {
				m.metrics.ObserveEventDelivered(event.ExecutionType, event.Type, time.Since(start))
			}
		default:
			// Channel is full, skip this client
			if m.metrics != nil {
				m.metrics.ObserveEventDropped(event.ExecutionType, event.Type)
			}
			logger.Warn("Client channel full, skipping event",
				zap.String("client_id", client.ClientID),
				zap.String("execution_id", event.ExecutionID),
			)
		}
	}
}

// GetSubscriberCount returns the number of subscribers for an execution.
func (m *EventStreamManager) GetSubscriberCount(executionID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients[executionID])
}

// cleanupInactiveClients removes clients that haven't been active for too long
func (m *EventStreamManager) cleanupInactiveClients() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		now := time.Now()

		for executionID, clients := range m.clients {
			activeClients := make([]*EventChannel, 0, len(clients))

			for _, client := range clients {
				// Remove clients inactive for more than 5 minutes
				if now.Sub(client.lastActiveTime()) < 5*time.Minute {
					activeClients = append(activeClients, client)
				} else {
					close(client.Channel)
					logger.Info("Removed inactive client",
						zap.String("execution_id", executionID),
						zap.String("client_id", client.ClientID),
					)
				}
			}

			if len(activeClients) == 0 {
				delete(m.clients, executionID)
			} else {
				m.clients[executionID] = activeClients
			}
		}

		m.mu.Unlock()
	}
}

// SendHeartbeat sends a heartbeat/comment to keep connection alive.
func (m *EventStreamManager) SendHeartbeat(executionID string) {
	event := ExecutionEvent{
		Type:        "heartbeat",
		ExecutionID: executionID,
		Timestamp:   time.Now(),
	}
	if m.metrics != nil {
		m.metrics.ObserveHeartbeat("")
	}
	m.Publish(event)
}

// FormatSSE formats an event in SSE format
func FormatSSE(event ExecutionEvent) string {
	data, err := json.Marshal(event)
	if err != nil {
		logger.Error("Failed to marshal event", zap.Error(err))
		return ""
	}

	// SSE format:
	// event: <event_type>
	// data: <json_data>
	// <blank line>
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, string(data))
}
