package mysql

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
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
	config Config
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
