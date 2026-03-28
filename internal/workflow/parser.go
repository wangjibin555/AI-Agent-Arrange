package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
	"gopkg.in/yaml.v3"
)

// Parser 负责把 YAML / JSON 工作流定义解析成 Workflow 结构，并做基础校验。
type Parser struct {
	report ValidationReport
}

// ValidationReport 保存解析阶段的非致命发现。
// 这类信息不会阻止执行，但能提示 DSL 是否有更合适的建模方式。
type ValidationReport struct {
	Warnings    []string `json:"warnings,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// NewParser 创建工作流解析器。
func NewParser() *Parser {
	return &Parser{}
}

// ValidateDefinition 使用与解析输入相同的规则校验工作流定义。
func ValidateDefinition(wf *Workflow) error {
	return NewParser().validate(wf)
}

// Warnings 返回最近一次解析得到的 warning 列表。
func (p *Parser) Warnings() []string {
	return append([]string(nil), p.report.Warnings...)
}

// LastReport 返回最近一次解析的完整报告。
func (p *Parser) LastReport() ValidationReport {
	return ValidationReport{
		Warnings:    append([]string(nil), p.report.Warnings...),
		Suggestions: append([]string(nil), p.report.Suggestions...),
	}
}

// ParseYAML 从 YAML 内容解析工作流。
func (p *Parser) ParseYAML(content []byte) (*Workflow, error) {
	p.resetWarnings()

	var wf Workflow
	if err := yaml.Unmarshal(content, &wf); err != nil {
		return nil, apperr.InvalidArgument("failed to parse YAML").WithCode("workflow_yaml_invalid").WithCause(err)
	}

	if err := p.validate(&wf); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return &wf, nil
}

// ParseYAMLCompiled 解析 YAML，并直接产出编译后的运行计划。
func (p *Parser) ParseYAMLCompiled(content []byte) (*CompiledWorkflow, error) {
	wf, err := p.ParseYAML(content)
	if err != nil {
		return nil, err
	}
	return CompileWorkflow(wf)
}

// ParseJSON 从 JSON 内容解析工作流。
func (p *Parser) ParseJSON(content []byte) (*Workflow, error) {
	p.resetWarnings()

	var wf Workflow
	if err := json.Unmarshal(content, &wf); err != nil {
		return nil, apperr.InvalidArgument("failed to parse JSON").WithCode("workflow_json_invalid").WithCause(err)
	}

	if err := p.validate(&wf); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return &wf, nil
}

// ParseJSONCompiled 解析 JSON，并直接产出编译后的运行计划。
func (p *Parser) ParseJSONCompiled(content []byte) (*CompiledWorkflow, error) {
	wf, err := p.ParseJSON(content)
	if err != nil {
		return nil, err
	}
	return CompileWorkflow(wf)
}

// ParseYAMLFile 从 YAML 文件解析工作流。
func (p *Parser) ParseYAMLFile(filepath string) (*Workflow, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.ParseYAML(content)
}

func (p *Parser) ParseYAMLFileCompiled(filepath string) (*CompiledWorkflow, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.ParseYAMLCompiled(content)
}

// ParseJSONFile 从 JSON 文件解析工作流。
func (p *Parser) ParseJSONFile(filepath string) (*Workflow, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.ParseJSON(content)
}

func (p *Parser) ParseJSONFileCompiled(filepath string) (*CompiledWorkflow, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return p.ParseJSONCompiled(content)
}

// validate 执行工作流定义的结构校验。
// 这里主要检查字段完整性、依赖存在性、route/foreach 规则以及 DAG 合法性。
func (p *Parser) validate(wf *Workflow) error {
	if wf.Name == "" {
		return apperr.InvalidArgument("workflow name is required").WithCode("workflow_name_required")
	}

	if len(wf.Steps) == 0 {
		return apperr.InvalidArgument("workflow must have at least one step").WithCode("workflow_steps_required")
	}

	// Validate steps
	stepIDs := make(map[string]bool)
	stepMap := make(map[string]*Step)
	for _, step := range wf.Steps {
		if step.ID == "" {
			return apperr.InvalidArgument("step ID is required").WithCode("workflow_step_id_required")
		}

		if stepIDs[step.ID] {
			return apperr.InvalidArgumentf("duplicate step ID: %s", step.ID).WithCode("workflow_step_id_duplicate")
		}
		stepIDs[step.ID] = true
		stepMap[step.ID] = step

		if step.AgentName == "" && step.Capability == "" {
			return apperr.InvalidArgumentf("step %s: agent name or capability is required", step.ID).WithCode("workflow_step_agent_required")
		}

		if step.Action == "" {
			return apperr.InvalidArgumentf("step %s: action is required", step.ID).WithCode("workflow_step_action_required")
		}

		// Validate dependencies exist
		for _, depID := range step.DependsOn {
			if !stepIDs[depID] && !p.willExist(depID, wf.Steps) {
				return apperr.InvalidArgumentf("step %s depends on non-existent step: %s", step.ID, depID).WithCode("workflow_dependency_not_found")
			}
		}
	}

	for _, step := range wf.Steps {
		if err := p.validateRoute(step, stepMap); err != nil {
			return err
		}
		if err := p.validateForeach(step); err != nil {
			return err
		}
	}

	p.collectConditionalBranchWarnings(wf)

	// Validate DAG structure (no cycles)
	if _, err := NewDAG(wf.Steps); err != nil {
		return apperr.InvalidArgument("invalid workflow structure").WithCode("workflow_structure_invalid").WithCause(err)
	}

	return nil
}

func (p *Parser) resetWarnings() {
	p.report = ValidationReport{}
}

func (p *Parser) addWarning(format string, args ...interface{}) {
	p.report.Warnings = append(p.report.Warnings, fmt.Sprintf(format, args...))
}

func (p *Parser) addSuggestion(format string, args ...interface{}) {
	p.report.Suggestions = append(p.report.Suggestions, fmt.Sprintf(format, args...))
}

// validateRoute 校验 route 的表达式、目标节点和依赖关系是否正确。
func (p *Parser) validateRoute(step *Step, stepMap map[string]*Step) error {
	if step.Route == nil {
		return nil
	}
	if step.Route.Expression == "" {
		return apperr.InvalidArgumentf("step %s: route expression is required", step.ID).WithCode("workflow_route_expression_required")
	}
	if len(step.Route.Cases) == 0 && len(step.Route.Default) == 0 {
		return apperr.InvalidArgumentf("step %s: route must define at least one case or default target", step.ID).WithCode("workflow_route_targets_required")
	}

	seenTargets := make(map[string]string)
	validateTargets := func(routeKey string, targets []string) error {
		for _, targetID := range targets {
			target, ok := stepMap[targetID]
			if !ok {
				return apperr.InvalidArgumentf("step %s: route target does not exist: %s", step.ID, targetID).WithCode("workflow_route_target_not_found")
			}
			if !slices.Contains(target.DependsOn, step.ID) {
				return apperr.InvalidArgumentf("step %s: route target %s must depend on router step", step.ID, targetID).WithCode("workflow_route_target_dependency_invalid")
			}
			if previousKey, exists := seenTargets[targetID]; exists {
				return apperr.InvalidArgumentf("step %s: route target %s is duplicated in %s and %s", step.ID, targetID, previousKey, routeKey).WithCode("workflow_route_target_duplicate")
			}
			seenTargets[targetID] = routeKey
		}
		return nil
	}

	for routeKey, targets := range step.Route.Cases {
		if err := validateTargets("case:"+routeKey, targets); err != nil {
			return err
		}
	}
	if err := validateTargets("default", step.Route.Default); err != nil {
		return err
	}

	return nil
}

// validateForeach 校验 foreach 的最小配置约束。
func (p *Parser) validateForeach(step *Step) error {
	if step.Foreach == nil {
		return nil
	}
	if step.Foreach.From == "" {
		return apperr.InvalidArgumentf("step %s: foreach.from is required", step.ID).WithCode("workflow_foreach_from_required")
	}
	if step.Foreach.ItemAs != "" && step.Foreach.IndexAs != "" && step.Foreach.ItemAs == step.Foreach.IndexAs {
		return apperr.InvalidArgumentf("step %s: foreach.item_as and foreach.index_as must be different", step.ID).WithCode("workflow_foreach_alias_conflict")
	}
	if step.Foreach.MaxParallel < 0 {
		return apperr.InvalidArgumentf("step %s: foreach.max_parallel must be >= 0", step.ID).WithCode("workflow_foreach_max_parallel_invalid")
	}
	if step.Streaming != nil && step.Streaming.Enabled {
		return apperr.InvalidArgumentf("step %s: foreach does not support streaming.enabled yet", step.ID).WithCode("workflow_foreach_streaming_unsupported")
	}
	return nil
}

// collectConditionalBranchWarnings 用启发式规则识别“看起来更像 route 的条件分支”。
func (p *Parser) collectConditionalBranchWarnings(wf *Workflow) {
	groups := make(map[string][]*Step)
	for _, step := range wf.Steps {
		if step.Condition == nil || step.Condition.Type != ConditionTypeExpression || len(step.DependsOn) == 0 {
			continue
		}
		key := dependencyGroupKey(step.DependsOn)
		groups[key] = append(groups[key], step)
	}

	for depKey, steps := range groups {
		if len(steps) < 2 {
			continue
		}

		stepIDs := make([]string, 0, len(steps))
		for _, step := range steps {
			stepIDs = append(stepIDs, step.ID)
		}
		sort.Strings(stepIDs)

		p.addWarning(
			"steps [%s] share the same dependencies [%s] and each uses expression condition; this looks like a branch split and is usually better modeled with route",
			strings.Join(stepIDs, ", "),
			depKey,
		)
		p.addSuggestion(
			"replace the conditional branch pattern on [%s] with a router step that sets route.expression and route.cases",
			strings.Join(stepIDs, ", "),
		)
	}
}

func dependencyGroupKey(dependsOn []string) string {
	deps := append([]string(nil), dependsOn...)
	sort.Strings(deps)
	return strings.Join(deps, ", ")
}

// willExist 判断某个步骤 ID 是否会在后续步骤中出现。
func (p *Parser) willExist(stepID string, steps []*Step) bool {
	for _, step := range steps {
		if step.ID == stepID {
			return true
		}
	}
	return false
}

// ToYAML 把工作流结构重新序列化为 YAML。
func (p *Parser) ToYAML(wf *Workflow) ([]byte, error) {
	data, err := yaml.Marshal(wf)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to YAML: %w", err)
	}
	return data, nil
}

// ToJSON 把工作流结构重新序列化为 JSON。
func (p *Parser) ToJSON(wf *Workflow) ([]byte, error) {
	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal to JSON: %w", err)
	}
	return data, nil
}
