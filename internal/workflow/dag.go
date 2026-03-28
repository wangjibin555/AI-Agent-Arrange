package workflow

import (
	"fmt"
	"sort"
)

// DAG 表示工作流步骤之间的有向无环图。
// 它只负责表达依赖关系，不处理 route、streaming 等更高层的运行时语义。
type DAG struct {
	nodes         map[string]*DAGNode // 当前节点对应步骤
	adjacencyList map[string][]string // 当前节点依赖的步骤ID列表
}

// DAGNode 表示 DAG 中的一个节点。
type DAGNode struct {
	StepID       string
	Step         *Step
	Dependencies []string // 当前节点依赖的步骤 ID 列表
	Dependents   []string // 依赖当前节点的步骤 ID 列表
}

// NewDAG 根据步骤列表构建 DAG，并校验依赖和环路。
func NewDAG(steps []*Step) (*DAG, error) {
	dag := &DAG{
		nodes:         make(map[string]*DAGNode),
		adjacencyList: make(map[string][]string),
	}

	// First pass: create nodes
	for _, step := range steps {
		if step.ID == "" {
			return nil, fmt.Errorf("step ID cannot be empty")
		}
		if _, exists := dag.nodes[step.ID]; exists {
			return nil, fmt.Errorf("duplicate step ID: %s", step.ID)
		}

		dag.nodes[step.ID] = &DAGNode{
			StepID:       step.ID,
			Step:         step,
			Dependencies: step.DependsOn,
			Dependents:   make([]string, 0),
		}
	}

	// Second pass: build adjacency list and validate dependencies
	for stepID, node := range dag.nodes {
		for _, depID := range node.Dependencies {
			// Check if dependency exists
			if _, exists := dag.nodes[depID]; !exists {
				return nil, fmt.Errorf("step %s depends on non-existent step: %s", stepID, depID)
			}

			// Add to adjacency list
			dag.adjacencyList[depID] = append(dag.adjacencyList[depID], stepID)

			// Add to dependent's list
			dag.nodes[depID].Dependents = append(dag.nodes[depID].Dependents, stepID)
		}
	}

	// Validate: check for cycles
	if err := dag.detectCycles(); err != nil {
		return nil, err
	}

	return dag, nil
}

// detectCycles 使用 DFS 检测 DAG 中是否存在环。
func (d *DAG) detectCycles() error {
	visited := make(map[string]bool)
	recStack := make(map[string]bool) // Recursion stack to track path

	var dfs func(stepID string) error
	dfs = func(stepID string) error {
		visited[stepID] = true
		recStack[stepID] = true

		for _, dependent := range d.adjacencyList[stepID] {
			if !visited[dependent] {
				if err := dfs(dependent); err != nil {
					return err
				}
			} else if recStack[dependent] {
				// Found a cycle
				return fmt.Errorf("cycle detected: step %s depends on itself (circular dependency)", dependent)
			}
		}

		recStack[stepID] = false
		return nil
	}

	for stepID := range d.nodes {
		if !visited[stepID] {
			if err := dfs(stepID); err != nil {
				return err
			}
		}
	}

	return nil
}

// GetExecutionOrder 返回拓扑排序后的执行层级。
// 同一层中的步骤互不依赖，因此理论上可以并行执行。
func (d *DAG) GetExecutionOrder() ([][]*Step, error) {
	// Calculate in-degree for each node
	inDegree := make(map[string]int)
	for stepID := range d.nodes {
		inDegree[stepID] = len(d.nodes[stepID].Dependencies)
	}

	// Queue of steps with in-degree 0 (no dependencies)
	queue := make([]string, 0)
	for stepID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, stepID)
		}
	}

	// Sort queue for deterministic ordering
	sort.Strings(queue)

	// Result: array of levels, each level can be executed in parallel
	var executionLevels [][]*Step
	visited := 0

	for len(queue) > 0 {
		// All steps in current queue can be executed in parallel
		currentLevel := make([]*Step, len(queue))
		for i, stepID := range queue {
			currentLevel[i] = d.nodes[stepID].Step
			visited++
		}
		executionLevels = append(executionLevels, currentLevel)

		// Process next level
		nextQueue := make([]string, 0)
		for _, stepID := range queue {
			// Reduce in-degree of all dependents
			for _, dependent := range d.adjacencyList[stepID] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					nextQueue = append(nextQueue, dependent)
				}
			}
		}

		// Sort next queue for deterministic ordering
		sort.Strings(nextQueue)
		queue = nextQueue
	}

	// Check if all nodes were visited (should be guaranteed by cycle detection)
	if visited != len(d.nodes) {
		return nil, fmt.Errorf("internal error: not all steps were processed (possible cycle)")
	}

	return executionLevels, nil
}

// GetRootSteps 返回没有依赖的起始步骤。
func (d *DAG) GetRootSteps() []*Step {
	roots := make([]*Step, 0)
	for stepID, node := range d.nodes {
		if len(node.Dependencies) == 0 {
			roots = append(roots, d.nodes[stepID].Step)
		}
	}

	// Sort for deterministic order
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].ID < roots[j].ID
	})

	return roots
}

// GetLeafSteps 返回没有下游依赖者的结束步骤。
func (d *DAG) GetLeafSteps() []*Step {
	leaves := make([]*Step, 0)
	for stepID, node := range d.nodes {
		if len(node.Dependents) == 0 {
			leaves = append(leaves, d.nodes[stepID].Step)
		}
	}

	// Sort for deterministic order
	sort.Slice(leaves, func(i, j int) bool {
		return leaves[i].ID < leaves[j].ID
	})

	return leaves
}

// GetStep 按 ID 获取步骤。
func (d *DAG) GetStep(stepID string) (*Step, bool) {
	node, exists := d.nodes[stepID]
	if !exists {
		return nil, false
	}
	return node.Step, true
}

// GetDependencies 返回某个步骤的直接依赖。
func (d *DAG) GetDependencies(stepID string) ([]*Step, error) {
	node, exists := d.nodes[stepID]
	if !exists {
		return nil, fmt.Errorf("step not found: %s", stepID)
	}

	deps := make([]*Step, len(node.Dependencies))
	for i, depID := range node.Dependencies {
		deps[i] = d.nodes[depID].Step
	}

	return deps, nil
}

// GetDependents 返回直接依赖某个步骤的下游节点。
func (d *DAG) GetDependents(stepID string) ([]*Step, error) {
	node, exists := d.nodes[stepID]
	if !exists {
		return nil, fmt.Errorf("step not found: %s", stepID)
	}

	dependents := make([]*Step, len(node.Dependents))
	for i, depID := range node.Dependents {
		dependents[i] = d.nodes[depID].Step
	}

	return dependents, nil
}

// CanExecute 判断一个步骤在给定 completed 集合下是否可以执行。
func (d *DAG) CanExecute(stepID string, completedSteps map[string]bool) bool {
	node, exists := d.nodes[stepID]
	if !exists {
		return false
	}

	// Check if all dependencies are completed
	for _, depID := range node.Dependencies {
		if !completedSteps[depID] {
			return false
		}
	}

	return true
}

// GetAllSteps 返回 DAG 中的全部步骤。
func (d *DAG) GetAllSteps() []*Step {
	steps := make([]*Step, 0, len(d.nodes))
	for _, node := range d.nodes {
		steps = append(steps, node.Step)
	}

	// Sort for deterministic order
	sort.Slice(steps, func(i, j int) bool {
		return steps[i].ID < steps[j].ID
	})

	return steps
}
