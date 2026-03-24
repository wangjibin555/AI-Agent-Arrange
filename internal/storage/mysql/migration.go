package mysql

import (
	"database/sql"
	"fmt"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// 数据库迁移
func (db *DB) Migrate() error {
	logger.Info("Running database migrations...")

	migrations := []struct {
		name string
		sql  string
	}{
		{
			name: "create_tasks_table",
			sql: `
CREATE TABLE IF NOT EXISTS tasks (
    id VARCHAR(36) PRIMARY KEY,
    agent_name VARCHAR(100) NOT NULL,
    required_capability VARCHAR(100),
    action VARCHAR(100) NOT NULL,
    parameters JSON,
    priority INT DEFAULT 0,
    timeout_seconds INT DEFAULT 60,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    result JSON,
    error TEXT,
    retry_count INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP NULL,
    completed_at TIMESTAMP NULL,
    INDEX idx_status (status),
    INDEX idx_agent_name (agent_name),
    INDEX idx_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`,
		},
		{
			name: "create_task_logs_table",
			sql: `
CREATE TABLE IF NOT EXISTS task_logs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    task_id VARCHAR(36) NOT NULL,
    level VARCHAR(20) NOT NULL,
    message TEXT NOT NULL,
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_task_id (task_id),
    INDEX idx_timestamp (timestamp),
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`,
		},
		{
			name: "create_workflows_table",
			sql: `
CREATE TABLE IF NOT EXISTS workflows (
    id VARCHAR(191) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    version VARCHAR(64),
    definition JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_workflows_updated_at (updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`,
		},
		{
			name: "create_workflow_executions_table",
			sql: `
CREATE TABLE IF NOT EXISTS workflow_executions (
    id VARCHAR(36) PRIMARY KEY,
    workflow_id VARCHAR(191) NOT NULL,
    status VARCHAR(20) NOT NULL,
    recovery_status VARCHAR(20) NOT NULL DEFAULT 'none',
    current_step VARCHAR(191),
    superseded_by_execution_id VARCHAR(36) NULL,
    error TEXT,
    context_json JSON,
    step_executions_json JSON,
    checkpoints_json JSON,
    resume_state_json JSON,
    route_selections_json JSON,
    started_at TIMESTAMP NOT NULL,
    completed_at TIMESTAMP NULL,
    last_heartbeat_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_workflow_executions_workflow_id (workflow_id),
    INDEX idx_workflow_executions_status (status),
    INDEX idx_workflow_executions_recovery_status (recovery_status),
    INDEX idx_workflow_executions_superseded_by (superseded_by_execution_id),
    INDEX idx_workflow_executions_updated_at (updated_at),
    CONSTRAINT fk_workflow_executions_workflow
        FOREIGN KEY (workflow_id) REFERENCES workflows(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`,
		},
	}

	for _, migration := range migrations {
		logger.Info("Applying migration", zap.String("name", migration.name))
		if _, err := db.Exec(migration.sql); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", migration.name, err)
		}
		logger.Info("Migration applied successfully", zap.String("name", migration.name))
	}

	if err := db.ensureWorkflowExecutionRecoveryColumns(); err != nil {
		return fmt.Errorf("failed to ensure workflow execution recovery columns: %w", err)
	}

	logger.Info("All migrations completed successfully")
	return nil
}

func (db *DB) ensureWorkflowExecutionRecoveryColumns() error {
	columnMigrations := []struct {
		column string
		sql    string
	}{
		{
			column: "recovery_status",
			sql:    "ALTER TABLE workflow_executions ADD COLUMN recovery_status VARCHAR(20) NOT NULL DEFAULT 'none' AFTER status",
		},
		{
			column: "superseded_by_execution_id",
			sql:    "ALTER TABLE workflow_executions ADD COLUMN superseded_by_execution_id VARCHAR(36) NULL AFTER current_step",
		},
	}

	for _, migration := range columnMigrations {
		exists, err := db.columnExists("workflow_executions", migration.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}

		logger.Info("Applying compatibility migration",
			zap.String("table", "workflow_executions"),
			zap.String("column", migration.column),
		)
		if _, err := db.Exec(migration.sql); err != nil {
			return fmt.Errorf("failed to add column %s: %w", migration.column, err)
		}
	}

	indexMigrations := []struct {
		index string
		sql   string
	}{
		{
			index: "idx_workflow_executions_recovery_status",
			sql:   "CREATE INDEX idx_workflow_executions_recovery_status ON workflow_executions (recovery_status)",
		},
		{
			index: "idx_workflow_executions_superseded_by",
			sql:   "CREATE INDEX idx_workflow_executions_superseded_by ON workflow_executions (superseded_by_execution_id)",
		},
	}

	for _, migration := range indexMigrations {
		exists, err := db.indexExists("workflow_executions", migration.index)
		if err != nil {
			return err
		}
		if exists {
			continue
		}

		logger.Info("Applying compatibility migration",
			zap.String("table", "workflow_executions"),
			zap.String("index", migration.index),
		)
		if _, err := db.Exec(migration.sql); err != nil {
			return fmt.Errorf("failed to add index %s: %w", migration.index, err)
		}
	}

	return nil
}

func (db *DB) columnExists(tableName, columnName string) (bool, error) {
	query := `
SELECT 1
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = ?
  AND COLUMN_NAME = ?
LIMIT 1
`

	var exists int
	err := db.QueryRow(query, tableName, columnName).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check column %s.%s: %w", tableName, columnName, err)
	}
	return true, nil
}

func (db *DB) indexExists(tableName, indexName string) (bool, error) {
	query := `
SELECT 1
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE()
  AND TABLE_NAME = ?
  AND INDEX_NAME = ?
LIMIT 1
`

	var exists int
	err := db.QueryRow(query, tableName, indexName).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check index %s.%s: %w", tableName, indexName, err)
	}
	return true, nil
}

// 删除当前项目数据库内部对应数据表，仅用于测试，进行数据隔离
func (db *DB) DropTables() error {
	logger.Warn("Dropping all tables (this will delete all data)")

	tables := []string{"workflow_executions", "workflows", "task_logs", "tasks"}
	for _, table := range tables {
		sql := fmt.Sprintf("DROP TABLE IF EXISTS %s", table)
		if _, err := db.Exec(sql); err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
		logger.Info("Table dropped", zap.String("table", table))
	}

	return nil
}
