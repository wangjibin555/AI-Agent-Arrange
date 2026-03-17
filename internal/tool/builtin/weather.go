package builtin

import (
	"context"
	"fmt"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// Weather is a tool for getting weather information
type Weather struct{}

// NewWeather creates a new weather tool
func NewWeather() *Weather {
	return &Weather{}
}

// GetDefinition returns the tool definition
func (w *Weather) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "get_weather",
		Description: "Get current weather information for a specific location (city or region).",
		Parameters: &tool.ParametersSchema{
			Type: "object",
			Properties: map[string]*tool.PropertySchema{
				"location": {
					Type:        "string",
					Description: "The city or location to get weather for, e.g., 'Beijing', 'New York', 'London'",
				},
				"unit": {
					Type:        "string",
					Description: "Temperature unit: 'celsius' or 'fahrenheit'. Default is 'celsius'.",
					Enum:        []string{"celsius", "fahrenheit"},
				},
			},
			Required: []string{"location"},
		},
	}
}

// Execute gets weather information
func (w *Weather) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	location, ok := params["location"].(string)
	if !ok || location == "" {
		return &tool.Result{
			Success: false,
			Error:   "missing or invalid 'location' parameter",
		}, fmt.Errorf("missing location")
	}

	unit := "celsius"
	if u, ok := params["unit"].(string); ok && u != "" {
		unit = u
	}

	// Mock weather data (in production, integrate with real weather API like OpenWeatherMap)
	weatherData := w.getMockWeather(location, unit)

	return &tool.Result{
		Success: true,
		Data:    weatherData,
	}, nil
}

// getMockWeather returns mock weather data
func (w *Weather) getMockWeather(location, unit string) map[string]interface{} {
	tempSymbol := "°C"
	temp := 22
	if unit == "fahrenheit" {
		tempSymbol = "°F"
		temp = 72
	}

	return map[string]interface{}{
		"location":    location,
		"temperature": fmt.Sprintf("%d%s", temp, tempSymbol),
		"condition":   "Partly Cloudy",
		"humidity":    "65%",
		"wind_speed":  "15 km/h",
		"description": fmt.Sprintf("Current weather in %s is partly cloudy with a temperature of %d%s", location, temp, tempSymbol),
		"note":        "This is mock data. Integrate with a real weather API (e.g., OpenWeatherMap) for production use.",
	}
}
