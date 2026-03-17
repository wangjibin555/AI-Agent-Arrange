package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// CurrentTime is a tool for getting current time
type CurrentTime struct{}

// NewCurrentTime creates a new current time tool
func NewCurrentTime() *CurrentTime {
	return &CurrentTime{}
}

// GetDefinition returns the tool definition
func (t *CurrentTime) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "get_current_time",
		Description: "Get the current date and time. Can return time in specific timezone or format.",
		Parameters: &tool.ParametersSchema{
			Type: "object",
			Properties: map[string]*tool.PropertySchema{
				"timezone": {
					Type:        "string",
					Description: "The timezone for the time, e.g., 'UTC', 'America/New_York', 'Asia/Shanghai'. Default is 'UTC'.",
				},
				"format": {
					Type:        "string",
					Description: "The format for the time. Use 'iso8601', 'unix', or 'readable'. Default is 'iso8601'.",
					Enum:        []string{"iso8601", "unix", "readable"},
				},
			},
			Required: []string{},
		},
	}
}

// Execute gets current time
func (t *CurrentTime) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	timezone := "UTC"
	if tz, ok := params["timezone"].(string); ok && tz != "" {
		timezone = tz
	}

	format := "iso8601"
	if f, ok := params["format"].(string); ok && f != "" {
		format = f
	}

	// Load timezone
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return &tool.Result{
			Success: false,
			Error:   fmt.Sprintf("invalid timezone: %v", err),
		}, err
	}

	now := time.Now().In(loc)

	var timeStr string
	switch format {
	case "unix":
		timeStr = fmt.Sprintf("%d", now.Unix())
	case "readable":
		timeStr = now.Format("Monday, January 2, 2006 at 3:04:05 PM MST")
	default: // iso8601
		timeStr = now.Format(time.RFC3339)
	}

	return &tool.Result{
		Success: true,
		Data: map[string]interface{}{
			"time":     timeStr,
			"timezone": timezone,
			"format":   format,
			"unix":     now.Unix(),
			"year":     now.Year(),
			"month":    int(now.Month()),
			"day":      now.Day(),
			"hour":     now.Hour(),
			"minute":   now.Minute(),
			"second":   now.Second(),
			"weekday":  now.Weekday().String(),
		},
	}, nil
}
