package api

import (
	"time"
)

// EventPublisher implements the TaskEventPublisher interface
// It adapts EventStreamManager to the orchestrator package interface
type EventPublisher struct {
	manager *EventStreamManager
}

// NewEventPublisher creates a new event publisher
func NewEventPublisher(manager *EventStreamManager) *EventPublisher {
	return &EventPublisher{
		manager: manager,
	}
}

// 发布事件
func (p *EventPublisher) PublishTaskEvent(
	taskID string,
	eventType string,
	status string,
	message string,
	result map[string]interface{},
	errorMsg string,
) {
	event := TaskEvent{
		Type:      eventType,
		TaskID:    taskID,
		Status:    status,
		Message:   message,
		Result:    result,
		Error:     errorMsg,
		Timestamp: time.Now(),
	}

	p.manager.Publish(event)
}
