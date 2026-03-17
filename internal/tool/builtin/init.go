package builtin

import "github.com/wangjibin555/AI-Agent-Arrange/internal/tool"

// RegisterAllBuiltinTools registers all built-in tools to the registry
func RegisterAllBuiltinTools(registry *tool.Registry) error {
	// Register calculator
	if err := registry.Register(NewCalculator()); err != nil {
		return err
	}

	// Register weather tool
	if err := registry.Register(NewWeather()); err != nil {
		return err
	}

	// Register current time tool
	if err := registry.Register(NewCurrentTime()); err != nil {
		return err
	}

	// Register web search tool
	if err := registry.Register(NewWebSearch()); err != nil {
		return err
	}

	return nil
}
