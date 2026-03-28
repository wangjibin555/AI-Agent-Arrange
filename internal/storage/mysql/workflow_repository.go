package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/internal/workflow"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/apperr"
)

// WorkflowRepository 负责将工作流定义和执行快照持久化到 MySQL。
type WorkflowRepository struct {
	db *DB
}

// NewWorkflowRepository 创建基于 MySQL 的工作流仓储实现。
func NewWorkflowRepository(db *DB) *WorkflowRepository {
	return &WorkflowRepository{db: db}
}

// SaveWorkflow 保存或更新工作流定义。
func (r *WorkflowRepository) SaveWorkflow(ctx context.Context, wf *workflow.Workflow) error {
	now := time.Now()
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = now
	}
	if wf.UpdatedAt.IsZero() {
		wf.UpdatedAt = now
	}

	definitionJSON, err := json.Marshal(wf)
	if err != nil {
		return fmt.Errorf("failed to marshal workflow definition: %w", err)
	}

	query := `
		INSERT INTO workflows (id, name, version, definition, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name),
			version = VALUES(version),
			definition = VALUES(definition),
			updated_at = VALUES(updated_at)
	`

	_, err = r.db.ExecWithContext(
		ctx,
		query,
		wf.ID,
		wf.Name,
		wf.Version,
		definitionJSON,
		wf.CreatedAt,
		wf.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save workflow %s: %w", wf.ID, err)
	}

	return nil
}

// GetWorkflow 按 ID 加载工作流定义。
func (r *WorkflowRepository) GetWorkflow(ctx context.Context, id string) (*workflow.Workflow, error) {
	row := r.db.QueryRowWithContext(ctx, `SELECT definition FROM workflows WHERE id = ?`, id)

	var definitionJSON []byte
	if err := row.Scan(&definitionJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow not found: %s", id)
		}
		return nil, fmt.Errorf("failed to query workflow %s: %w", id, err)
	}

	var wf workflow.Workflow
	if err := json.Unmarshal(definitionJSON, &wf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow %s: %w", id, err)
	}

	return &wf, nil
}

// ListWorkflows 返回所有已保存的工作流定义。
func (r *WorkflowRepository) ListWorkflows(ctx context.Context) ([]*workflow.Workflow, error) {
	rows, err := r.db.QueryWithContext(ctx, `SELECT definition FROM workflows ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query workflows: %w", err)
	}
	defer rows.Close()

	var workflows []*workflow.Workflow
	for rows.Next() {
		var definitionJSON []byte
		if err := rows.Scan(&definitionJSON); err != nil {
			return nil, fmt.Errorf("failed to scan workflow definition: %w", err)
		}

		var wf workflow.Workflow
		if err := json.Unmarshal(definitionJSON, &wf); err != nil {
			return nil, fmt.Errorf("failed to unmarshal workflow definition: %w", err)
		}
		workflows = append(workflows, &wf)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate workflows: %w", err)
	}

	return workflows, nil
}

// DeleteWorkflow 删除指定工作流定义。
func (r *WorkflowRepository) DeleteWorkflow(ctx context.Context, id string) error {
	result, err := r.db.ExecWithContext(ctx, `DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("failed to delete workflow %s: %w", id, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect workflow delete result: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("workflow not found: %s", id)
	}

	return nil
}

// SaveExecution 保存或更新一次工作流执行快照。
func (r *WorkflowRepository) SaveExecution(ctx context.Context, execution *workflow.WorkflowExecution) error {
	contextJSON, err := marshalJSON(execution.Context)
	if err != nil {
		return fmt.Errorf("failed to marshal execution context: %w", err)
	}
	stepExecutionsJSON, err := marshalJSON(execution.StepExecutions)
	if err != nil {
		return fmt.Errorf("failed to marshal step executions: %w", err)
	}
	checkpointsJSON, err := marshalJSON(execution.Checkpoints)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoints: %w", err)
	}
	resumeStateJSON, err := marshalJSON(execution.ResumeState)
	if err != nil {
		return fmt.Errorf("failed to marshal resume state: %w", err)
	}
	routeSelectionsJSON, err := marshalJSON(execution.RouteSelections)
	if err != nil {
		return fmt.Errorf("failed to marshal route selections: %w", err)
	}

	query := `
		INSERT INTO workflow_executions (
			id, workflow_id, status, recovery_status, current_step, superseded_by_execution_id, error,
			context_json, step_executions_json, checkpoints_json,
			resume_state_json, route_selections_json,
			started_at, completed_at, last_heartbeat_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE
			workflow_id = VALUES(workflow_id),
			status = VALUES(status),
			recovery_status = VALUES(recovery_status),
			current_step = VALUES(current_step),
			superseded_by_execution_id = VALUES(superseded_by_execution_id),
			error = VALUES(error),
			context_json = VALUES(context_json),
			step_executions_json = VALUES(step_executions_json),
			checkpoints_json = VALUES(checkpoints_json),
			resume_state_json = VALUES(resume_state_json),
			route_selections_json = VALUES(route_selections_json),
			started_at = VALUES(started_at),
			completed_at = VALUES(completed_at),
			last_heartbeat_at = NOW()
	`

	_, err = r.db.ExecWithContext(
		ctx,
		query,
		execution.ID,
		execution.WorkflowID,
		execution.Status,
		execution.RecoveryStatus,
		execution.CurrentStep,
		execution.SupersededByExecutionID,
		execution.Error,
		contextJSON,
		stepExecutionsJSON,
		checkpointsJSON,
		resumeStateJSON,
		routeSelectionsJSON,
		execution.StartedAt,
		execution.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save execution %s: %w", execution.ID, err)
	}

	return nil
}

// GetExecution 加载单个工作流执行快照。
func (r *WorkflowRepository) GetExecution(ctx context.Context, id string) (*workflow.WorkflowExecution, error) {
	row := r.db.QueryRowWithContext(ctx, `
		SELECT
			id, workflow_id, status, recovery_status, current_step, superseded_by_execution_id, error,
			context_json, step_executions_json, checkpoints_json,
			resume_state_json, route_selections_json,
			started_at, completed_at
		FROM workflow_executions
		WHERE id = ?
	`, id)

	execution, err := scanExecution(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, apperr.NotFoundf("execution not found: %s", id).WithCode("execution_not_found")
		}
		return nil, fmt.Errorf("failed to query execution %s: %w", id, err)
	}

	return execution, nil
}

// ListExecutions returns executions for one workflow.
func (r *WorkflowRepository) ListExecutions(ctx context.Context, workflowID string) ([]*workflow.WorkflowExecution, error) {
	rows, err := r.db.QueryWithContext(ctx, `
		SELECT
			id, workflow_id, status, recovery_status, current_step, superseded_by_execution_id, error,
			context_json, step_executions_json, checkpoints_json,
			resume_state_json, route_selections_json,
			started_at, completed_at
		FROM workflow_executions
		WHERE workflow_id = ?
		ORDER BY started_at DESC
	`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to query executions for workflow %s: %w", workflowID, err)
	}
	defer rows.Close()

	return scanExecutionRows(rows)
}

// GetRunningExecutions returns all executions still marked as running.
func (r *WorkflowRepository) GetRunningExecutions(ctx context.Context) ([]*workflow.WorkflowExecution, error) {
	rows, err := r.db.QueryWithContext(ctx, `
		SELECT
			id, workflow_id, status, recovery_status, current_step, superseded_by_execution_id, error,
			context_json, step_executions_json, checkpoints_json,
			resume_state_json, route_selections_json,
			started_at, completed_at
		FROM workflow_executions
		WHERE status = ?
		ORDER BY started_at ASC
	`, workflow.WorkflowStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("failed to query running executions: %w", err)
	}
	defer rows.Close()

	return scanExecutionRows(rows)
}

func scanExecutionRows(rows *sql.Rows) ([]*workflow.WorkflowExecution, error) {
	executions := make([]*workflow.WorkflowExecution, 0)
	for rows.Next() {
		execution, err := scanExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan execution row: %w", err)
		}
		executions = append(executions, execution)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate execution rows: %w", err)
	}

	return executions, nil
}

type executionRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanExecution(scanner executionRowScanner) (*workflow.WorkflowExecution, error) {
	var (
		execution           workflow.WorkflowExecution
		contextJSON         []byte
		stepExecutionsJSON  []byte
		checkpointsJSON     []byte
		resumeStateJSON     []byte
		routeSelectionsJSON []byte
	)

	err := scanner.Scan(
		&execution.ID,
		&execution.WorkflowID,
		&execution.Status,
		&execution.RecoveryStatus,
		&execution.CurrentStep,
		&execution.SupersededByExecutionID,
		&execution.Error,
		&contextJSON,
		&stepExecutionsJSON,
		&checkpointsJSON,
		&resumeStateJSON,
		&routeSelectionsJSON,
		&execution.StartedAt,
		&execution.CompletedAt,
	)
	if err != nil {
		return nil, err
	}

	if err := unmarshalJSON(contextJSON, &execution.Context); err != nil {
		return nil, fmt.Errorf("failed to unmarshal execution context: %w", err)
	}
	if execution.Context == nil {
		execution.Context = workflow.NewExecutionContext(nil)
	}
	if execution.RecoveryStatus == "" {
		execution.RecoveryStatus = workflow.RecoveryStatusNone
	}

	if err := unmarshalJSON(stepExecutionsJSON, &execution.StepExecutions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal step executions: %w", err)
	}
	if execution.StepExecutions == nil {
		execution.StepExecutions = make(map[string]*workflow.StepExecution)
	}

	if err := unmarshalJSON(checkpointsJSON, &execution.Checkpoints); err != nil {
		return nil, fmt.Errorf("failed to unmarshal checkpoints: %w", err)
	}
	if execution.Checkpoints == nil {
		execution.Checkpoints = make(map[string]*workflow.StreamCheckpoint)
	}

	if err := unmarshalJSON(resumeStateJSON, &execution.ResumeState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal resume state: %w", err)
	}
	if err := unmarshalJSON(routeSelectionsJSON, &execution.RouteSelections); err != nil {
		return nil, fmt.Errorf("failed to unmarshal route selections: %w", err)
	}
	if execution.RouteSelections == nil {
		execution.RouteSelections = make(map[string]string)
	}

	return &execution, nil
}

func marshalJSON(v interface{}) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func unmarshalJSON(data []byte, target interface{}) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}
