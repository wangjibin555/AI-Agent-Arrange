package api

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// 具体任务事件对象，负责存储
type TaskEvent struct {
	Type      string                 `json:"type"`      // event type: status_changed, progress, completed, failed
	TaskID    string                 `json:"task_id"`   // task ID
	Status    string                 `json:"status"`    // task status
	Progress  int                    `json:"progress"`  // progress percentage (0-100)
	Message   string                 `json:"message"`   // human-readable message
	Result    map[string]interface{} `json:"result"`    // task result (for completed events)
	Error     string                 `json:"error"`     // error message (for failed events)
	Timestamp time.Time              `json:"timestamp"` // event timestamp
}

// SSE连接对象
type EventChannel struct {
	TaskID     string
	ClientID   string
	Channel    chan TaskEvent
	LastActive time.Time
}

// EventStreamManager manages SSE connections and event distribution
type EventStreamManager struct {
	clients map[string][]*EventChannel // taskID -> list of client channels
	mu      sync.RWMutex
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

// Subscribe subscribes a client to task events
func (m *EventStreamManager) Subscribe(taskID string, clientID string) *EventChannel {
	m.mu.Lock()
	defer m.mu.Unlock()

	channel := &EventChannel{
		TaskID:     taskID,
		ClientID:   clientID,
		Channel:    make(chan TaskEvent, 10), // buffered channel
		LastActive: time.Now(),
	}

	m.clients[taskID] = append(m.clients[taskID], channel)

	logger.Info("Client subscribed to task events",
		zap.String("task_id", taskID),
		zap.String("client_id", clientID),
		zap.Int("total_clients", len(m.clients[taskID])),
	)

	return channel
}

// Unsubscribe unsubscribes a client from task events
func (m *EventStreamManager) Unsubscribe(taskID string, clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	clients := m.clients[taskID]
	for i, client := range clients {
		if client.ClientID == clientID {
			close(client.Channel)
			m.clients[taskID] = append(clients[:i], clients[i+1:]...)

			logger.Info("Client unsubscribed from task events",
				zap.String("task_id", taskID),
				zap.String("client_id", clientID),
				zap.Int("remaining_clients", len(m.clients[taskID])),
			)

			// Remove task entry if no clients left
			if len(m.clients[taskID]) == 0 {
				delete(m.clients, taskID)
			}
			break
		}
	}
}

// Publish publishes an event to all subscribers of a task
func (m *EventStreamManager) Publish(event TaskEvent) {
	m.mu.RLock()
	clients := m.clients[event.TaskID]
	m.mu.RUnlock()

	if len(clients) == 0 {
		return // no subscribers
	}

	logger.Debug("Publishing event",
		zap.String("task_id", event.TaskID),
		zap.String("type", event.Type),
		zap.Int("subscribers", len(clients)),
	)

	for _, client := range clients {
		select {
		case client.Channel <- event:
			client.LastActive = time.Now()
		default:
			// Channel is full, skip this client
			logger.Warn("Client channel full, skipping event",
				zap.String("client_id", client.ClientID),
				zap.String("task_id", event.TaskID),
			)
		}
	}
}

// GetSubscriberCount returns the number of subscribers for a task
func (m *EventStreamManager) GetSubscriberCount(taskID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients[taskID])
}

// cleanupInactiveClients removes clients that haven't been active for too long
func (m *EventStreamManager) cleanupInactiveClients() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()

		for taskID, clients := range m.clients {
			activeClients := make([]*EventChannel, 0, len(clients))

			for _, client := range clients {
				// Remove clients inactive for more than 5 minutes
				if time.Since(client.LastActive) < 5*time.Minute {
					activeClients = append(activeClients, client)
				} else {
					close(client.Channel)
					logger.Info("Removed inactive client",
						zap.String("task_id", taskID),
						zap.String("client_id", client.ClientID),
					)
				}
			}

			if len(activeClients) == 0 {
				delete(m.clients, taskID)
			} else {
				m.clients[taskID] = activeClients
			}
		}

		m.mu.Unlock()
	}
}

// SendHeartbeat sends a heartbeat/comment to keep connection alive
func (m *EventStreamManager) SendHeartbeat(taskID string) {
	event := TaskEvent{
		Type:      "heartbeat",
		TaskID:    taskID,
		Timestamp: time.Now(),
	}
	m.Publish(event)
}

// FormatSSE formats an event in SSE format
func FormatSSE(event TaskEvent) string {
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
