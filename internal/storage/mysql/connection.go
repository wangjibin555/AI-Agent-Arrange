package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/wangjibin555/AI-Agent-Arrange/internal/monitor"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
	"go.uber.org/zap"
)

// Config represents MySQL connection configuration
type Config struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DB wraps sql.DB with additional functionality
type DB struct {
	*sql.DB
	config  Config
	metrics *monitor.DependencyMetrics
}

// NewDB creates a new MySQL database connection
func NewDB(config Config) (*DB, error) {
	// Build DSN (Data Source Name)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		config.User,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
	)

	logger.Info("Connecting to MySQL database",
		zap.String("host", config.Host),
		zap.Int("port", config.Port),
		zap.String("database", config.Database),
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	if config.MaxOpenConns == 0 {
		config.MaxOpenConns = 50 // Default
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = 10 // Default
	}
	if config.ConnMaxLifetime == 0 {
		config.ConnMaxLifetime = time.Hour // Default
	}

	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Info("Successfully connected to MySQL database",
		zap.Int("max_open_conns", config.MaxOpenConns),
		zap.Int("max_idle_conns", config.MaxIdleConns),
	)

	return &DB{
		DB:     db,
		config: config,
	}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	logger.Info("Closing MySQL database connection")
	return db.DB.Close()
}

// HealthCheck checks if the database connection is alive
func (db *DB) HealthCheck() error {
	return db.Ping()
}

func (db *DB) SetDependencyMetrics(metrics *monitor.DependencyMetrics) {
	db.metrics = metrics
}

// ExecWithContext wraps ExecContext with dependency-level structured logging.
func (db *DB) ExecWithContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	result, err := db.DB.ExecContext(ctx, query, args...)
	db.logOperation(ctx, "exec", query, start, err)
	return result, err
}

// QueryWithContext wraps QueryContext with dependency-level structured logging.
func (db *DB) QueryWithContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := db.DB.QueryContext(ctx, query, args...)
	db.logOperation(ctx, "query", query, start, err)
	return rows, err
}

// QueryRowWithContext wraps QueryRowContext with dependency-level structured logging.
func (db *DB) QueryRowWithContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	start := time.Now()
	row := db.DB.QueryRowContext(ctx, query, args...)
	db.logOperation(ctx, "query_row", query, start, nil)
	return row
}

// PingWithContext wraps PingContext with dependency-level structured logging.
func (db *DB) PingWithContext(ctx context.Context) error {
	start := time.Now()
	err := db.DB.PingContext(ctx)
	db.logOperation(ctx, "ping", "PING", start, err)
	return err
}

func (db *DB) logOperation(ctx context.Context, operation string, query string, start time.Time, err error) {
	fields := []zap.Field{
		zap.String("component", "mysql"),
		zap.String("operation", operation),
		zap.Duration("latency", time.Since(start)),
		zap.String("database", db.config.Database),
		zap.String("query_preview", previewQuery(query)),
	}

	if meta, ok := midware.RequestMetadataFromContext(ctx); ok {
		if meta.RequestID != "" {
			fields = append(fields, zap.String("request_id", meta.RequestID))
		}
		if meta.TraceID != "" {
			fields = append(fields, zap.String("trace_id", meta.TraceID))
		}
		if meta.UserID != "" {
			fields = append(fields, zap.String("user_id", meta.UserID))
		}
		if meta.TenantID != "" {
			fields = append(fields, zap.String("tenant_id", meta.TenantID))
		}
	}

	if err != nil {
		if db.metrics != nil {
			db.metrics.Observe("db", db.config.Database, operation, sqlStatus(err), time.Since(start), 150*time.Millisecond)
		}
		fields = append(fields, zap.Error(err))
		logger.Warn("MySQL operation failed", fields...)
		return
	}
	if db.metrics != nil {
		db.metrics.Observe("db", db.config.Database, operation, "success", time.Since(start), 150*time.Millisecond)
	}

	logger.Debug("MySQL operation completed", fields...)
}

func previewQuery(query string) string {
	if len(query) <= 120 {
		return query
	}
	return query[:120] + "..."
}

func sqlStatus(err error) string {
	switch err {
	case nil:
		return "success"
	case context.DeadlineExceeded:
		return "timeout"
	default:
		return "error"
	}
}
