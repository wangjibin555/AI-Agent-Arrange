package agent

import "github.com/sashabaranov/go-openai"

const (
	EventLLMStreamChunk      = "llm_stream_chunk"
	EventLLMResponseComplete = "llm_response_complete"
	EventToolCallPrepared    = "tool_call_prepared"
	EventToolCallStarted     = "tool_call_started"
	EventToolCallFinished    = "tool_call_finished"
)

func publishLLMStreamChunk(input *TaskInput, iteration int, delta, content string) {
	if input == nil || input.EventPublisher == nil {
		return
	}
	input.EventPublisher.PublishTaskEvent(
		input.TaskID,
		EventLLMStreamChunk,
		"running",
		"",
		map[string]interface{}{
			"content_delta": delta,
			"content":       content,
			"iteration":     iteration,
		},
		"",
	)
}

func publishLLMResponseComplete(input *TaskInput, iteration int, content, finishReason string) {
	if input == nil || input.EventPublisher == nil {
		return
	}
	input.EventPublisher.PublishTaskEvent(
		input.TaskID,
		EventLLMResponseComplete,
		"running",
		"",
		map[string]interface{}{
			"content":       content,
			"finish_reason": finishReason,
			"iteration":     iteration,
		},
		"",
	)
}

func publishToolCallPrepared(input *TaskInput, index int, tc openai.ToolCall) {
	if input == nil || input.EventPublisher == nil {
		return
	}
	input.EventPublisher.PublishTaskEvent(
		input.TaskID,
		EventToolCallPrepared,
		"running",
		"",
		map[string]interface{}{
			"tool_call_id": tc.ID,
			"tool_name":    tc.Function.Name,
			"arguments":    tc.Function.Arguments,
			"index":        index,
		},
		"",
	)
}

func publishToolCallStarted(input *TaskInput, index int, tc openai.ToolCall) {
	if input == nil || input.EventPublisher == nil {
		return
	}
	input.EventPublisher.PublishTaskEvent(
		input.TaskID,
		EventToolCallStarted,
		"running",
		"",
		map[string]interface{}{
			"tool_call_id": tc.ID,
			"tool_name":    tc.Function.Name,
			"arguments":    tc.Function.Arguments,
			"index":        index,
		},
		"",
	)
}

func publishToolCallFinished(input *TaskInput, index int, tc openai.ToolCall, result map[string]interface{}, err error) {
	if input == nil || input.EventPublisher == nil {
		return
	}
	data := map[string]interface{}{
		"tool_call_id": tc.ID,
		"tool_name":    tc.Function.Name,
		"index":        index,
		"success":      err == nil,
	}
	if err != nil {
		data["error"] = err.Error()
	} else {
		data["result"] = result
	}
	input.EventPublisher.PublishTaskEvent(
		input.TaskID,
		EventToolCallFinished,
		"running",
		"",
		data,
		"",
	)
}
