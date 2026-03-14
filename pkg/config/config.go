package config

import "time"

// Config represents the application configuration
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Orchestrator OrchestratorConfig `yaml:"orchestrator"`
	Redis        RedisConfig        `yaml:"redis"`
	MySQL        MySQLConfig        `yaml:"mysql"`
	RabbitMQ     RabbitMQConfig     `yaml:"rabbitmq"`
	Milvus       MilvusConfig       `yaml:"milvus"`
	LLM          LLMConfig          `yaml:"llm"`
	Monitoring   MonitoringConfig   `yaml:"monitoring"`
	Logging      LoggingConfig      `yaml:"logging"`
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"` // development, production
}

// OrchestratorConfig holds orchestrator engine configuration
type OrchestratorConfig struct {
	MaxConcurrentTasks  int  `yaml:"max_concurrent_tasks"`
	TaskTimeout         int  `yaml:"task_timeout"` // seconds
	EnableDAGValidation bool `yaml:"enable_dag_validation"`
}

// RedisConfig holds Redis connection configuration
type RedisConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	PoolSize int    `yaml:"pool_size"`
}

// MySQLConfig holds MySQL connection configuration
type MySQLConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	Database     string `yaml:"database"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
	MaxOpenConns int    `yaml:"max_open_conns"`
}

// RabbitMQConfig holds RabbitMQ connection configuration
type RabbitMQConfig struct {
	Host         string            `yaml:"host"`
	Port         int               `yaml:"port"`
	Username     string            `yaml:"username"`
	Password     string            `yaml:"password"`
	VHost        string            `yaml:"vhost"`
	Queues       map[string]string `yaml:"queues"`
	Exchange     string            `yaml:"exchange"`
	ExchangeType string            `yaml:"exchange_type"`
}

// MilvusConfig holds Milvus vector database configuration
type MilvusConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	CollectionName string `yaml:"collection_name"`
}

// LLMConfig holds LLM provider configuration
type LLMConfig struct {
	DefaultProvider string                    `yaml:"default_provider"`
	Providers       map[string]ProviderConfig `yaml:"providers"`
}

// ProviderConfig holds individual LLM provider settings
type ProviderConfig struct {
	APIKey      string  `yaml:"api_key"`
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
	BaseURL     string  `yaml:"base_url"`
}

// MonitoringConfig holds monitoring configuration
type MonitoringConfig struct {
	Prometheus PrometheusConfig `yaml:"prometheus"`
	Tracing    TracingConfig    `yaml:"tracing"`
}

// PrometheusConfig holds Prometheus configuration
type PrometheusConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// TracingConfig holds tracing configuration
type TracingConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level    string `yaml:"level"`  // debug, info, warn, error
	Format   string `yaml:"format"` // json, console
	Output   string `yaml:"output"` // stdout, file
	FilePath string `yaml:"file_path"`
}

// GetAddress returns the server address
func (s *ServerConfig) GetAddress() string {
	return s.Host + ":" + string(rune(s.Port))
}

// GetTaskTimeout returns task timeout as duration
func (o *OrchestratorConfig) GetTaskTimeout() time.Duration {
	return time.Duration(o.TaskTimeout) * time.Second
}

// GetDSN returns MySQL connection DSN
func (m *MySQLConfig) GetDSN() string {
	return m.Username + ":" + m.Password + "@tcp(" + m.Host + ":" + string(rune(m.Port)) + ")/" + m.Database + "?charset=utf8mb4&parseTime=True&loc=Local"
}

// GetRedisAddress returns Redis address
func (r *RedisConfig) GetRedisAddress() string {
	return r.Host + ":" + string(rune(r.Port))
}
