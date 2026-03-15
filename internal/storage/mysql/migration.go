package mysql

import (
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
	}

	for _, migration := range migrations {
		logger.Info("Applying migration", zap.String("name", migration.name))
		if _, err := db.Exec(migration.sql); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", migration.name, err)
		}
		logger.Info("Migration applied successfully", zap.String("name", migration.name))
	}

	logger.Info("All migrations completed successfully")
	return nil
}

// 删除当前项目数据库内部对应数据表，仅用于测试，进行数据隔离
func (db *DB) DropTables() error {
	logger.Warn("Dropping all tables (this will delete all data)")

	tables := []string{"task_logs", "tasks"}
	for _, table := range tables {
		sql := fmt.Sprintf("DROP TABLE IF EXISTS %s", table)
		if _, err := db.Exec(sql); err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
		logger.Info("Table dropped", zap.String("table", table))
	}

	return nil
}
