package api

import "time"

// UnifiedEventPublisher adapts the shared event manager to both task and workflow event interfaces.
type UnifiedEventPublisher struct {
	manager *EventStreamManager
}

// NewUnifiedEventPublisher creates a new unified event publisher.
func NewUnifiedEventPublisher(manager *EventStreamManager) *UnifiedEventPublisher {
	return &UnifiedEventPublisher{manager: manager}
}

// PublishTaskEvent implements orchestrator.TaskEventPublisher.
func (p *UnifiedEventPublisher) PublishTaskEvent(
	taskID string,
	eventType string,
	status string,
	message string,
	result map[string]interface{},
	errorMsg string,
) {
	p.manager.Publish(ExecutionEvent{
		Type:          eventType,
		ExecutionID:   taskID,
		ExecutionType: "task",
		Status:        status,
		Message:       message,
		Result:        result,
		Error:         errorMsg,
		Timestamp:     time.Now(),
	})
}

// PublishWorkflowEvent implements workflow.EventPublisher.
func (p *UnifiedEventPublisher) PublishWorkflowEvent(
	executionID string,
	eventType string,
	status string,
	message string,
	data map[string]interface{},
) {
	var result map[string]interface{}
	var errorMsg string
	var recoveryStatus string
	var supersededByExecutionID string
	if data != nil {
		if raw, ok := data["result"].(map[string]interface{}); ok {
			result = raw
		}
		if raw, ok := data["error"].(string); ok {
			errorMsg = raw
		}
		if raw, ok := data["recovery_status"].(string); ok {
			recoveryStatus = raw
		}
		if raw, ok := data["superseded_by_execution_id"].(string); ok {
			supersededByExecutionID = raw
		}
	}

	p.manager.Publish(ExecutionEvent{
		Type:                    eventType,
		ExecutionID:             executionID,
		ExecutionType:           "workflow",
		Status:                  status,
		RecoveryStatus:          recoveryStatus,
		SupersededByExecutionID: supersededByExecutionID,
		Message:                 message,
		Result:                  result,
		Error:                   errorMsg,
		Data:                    data,
		Timestamp:               time.Now(),
	})
}
