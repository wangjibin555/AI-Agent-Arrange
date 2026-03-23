package workflow

import (
	"strings"
	"testing"
)

func TestParser_WarnsForConditionalBranchPattern(t *testing.T) {
	parser := NewParser()

	wf, err := parser.ParseYAML([]byte(`
name: "conditional-branch"
steps:
  - id: initial_analysis
    agent: "test-agent"
    action: "analyze"

  - id: quick_summary
    agent: "test-agent"
    action: "summarize"
    depends_on: ["initial_analysis"]
    condition:
      type: "expression"
      expression: "{{initial_analysis.result.confidence}} > 0.7"

  - id: detailed_analysis
    agent: "test-agent"
    action: "analyze"
    depends_on: ["initial_analysis"]
    condition:
      type: "expression"
      expression: "{{initial_analysis.result.confidence}} <= 0.7"
`))
	if err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	if wf == nil {
		t.Fatalf("expected workflow")
	}

	report := parser.LastReport()
	if len(report.Warnings) == 0 {
		t.Fatalf("expected warnings for conditional branch pattern")
	}
	if !strings.Contains(report.Warnings[0], "better modeled with route") {
		t.Fatalf("expected route guidance warning, got %q", report.Warnings[0])
	}
	if len(report.Suggestions) == 0 {
		t.Fatalf("expected route suggestion for conditional branch pattern")
	}
}

func TestParser_NoWarningForSingleConditionalGuard(t *testing.T) {
	parser := NewParser()

	_, err := parser.ParseYAML([]byte(`
name: "single-guard"
steps:
  - id: prepare
    agent: "test-agent"
    action: "prepare"

  - id: optional_step
    agent: "test-agent"
    action: "process"
    depends_on: ["prepare"]
    condition:
      type: "expression"
      expression: "{{prepare.result.enabled}} == true"
`))
	if err != nil {
		t.Fatalf("parse yaml: %v", err)
	}

	report := parser.LastReport()
	if len(report.Warnings) != 0 || len(report.Suggestions) != 0 {
		t.Fatalf("expected empty validation report for single conditional guard, got %+v", report)
	}
}
