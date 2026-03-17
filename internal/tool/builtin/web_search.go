package builtin

import (
	"context"
	"fmt"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/tool"
)

// WebSearch is a tool for searching the web
type WebSearch struct{}

// NewWebSearch creates a new web search tool
func NewWebSearch() *WebSearch {
	return &WebSearch{}
}

// GetDefinition returns the tool definition
func (s *WebSearch) GetDefinition() *tool.Definition {
	return &tool.Definition{
		Name:        "web_search",
		Description: "Search the web for information using a search query. Returns relevant results with titles, URLs, and snippets.",
		Parameters: &tool.ParametersSchema{
			Type: "object",
			Properties: map[string]*tool.PropertySchema{
				"query": {
					Type:        "string",
					Description: "The search query to look up on the web",
				},
				"num_results": {
					Type:        "string",
					Description: "Number of results to return (default: 5, max: 10)",
				},
			},
			Required: []string{"query"},
		},
	}
}

// Execute performs web search
func (s *WebSearch) Execute(ctx context.Context, params map[string]interface{}) (*tool.Result, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return &tool.Result{
			Success: false,
			Error:   "missing or invalid 'query' parameter",
		}, fmt.Errorf("missing query")
	}

	numResults := 5
	if num, ok := params["num_results"].(float64); ok {
		numResults = int(num)
		if numResults > 10 {
			numResults = 10
		}
	}

	// Mock search results (in production, integrate with real search API like Google Custom Search or Bing)
	results := s.getMockSearchResults(query, numResults)

	return &tool.Result{
		Success: true,
		Data: map[string]interface{}{
			"query":       query,
			"num_results": len(results),
			"results":     results,
			"note":        "This is mock data. Integrate with a real search API (e.g., Google Custom Search, Bing API) for production use.",
		},
	}, nil
}

// getMockSearchResults returns mock search results
func (s *WebSearch) getMockSearchResults(query string, numResults int) []map[string]interface{} {
	results := make([]map[string]interface{}, 0, numResults)

	for i := 0; i < numResults; i++ {
		results = append(results, map[string]interface{}{
			"title":   fmt.Sprintf("Result %d for '%s'", i+1, query),
			"url":     fmt.Sprintf("https://example.com/result-%d", i+1),
			"snippet": fmt.Sprintf("This is a mock search result snippet for '%s'. It contains relevant information about the query.", query),
		})
	}

	return results
}
