package messaging

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"github.com/wangjibin555/AI-Agent-Arrange/pkg/midware"
	"go.uber.org/zap"
)

// RabbitMQConfig holds RabbitMQ configuration
type RabbitMQConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	VHost        string `yaml:"vhost"`
	Exchange     string `yaml:"exchange"`
	ExchangeType string `yaml:"exchange_type"`
}

// RabbitMQClient represents a RabbitMQ client
type RabbitMQClient struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	config  *RabbitMQConfig
}

// NewRabbitMQClient creates a new RabbitMQ client
func NewRabbitMQClient(config *RabbitMQConfig) (*RabbitMQClient, error) {
	url := fmt.Sprintf("amqp://%s:%s@%s:%d%s",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.VHost,
	)

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	channel, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	// Declare exchange
	err = channel.ExchangeDeclare(
		config.Exchange,     // name
		config.ExchangeType, // type
		true,                // durable
		false,               // auto-deleted
		false,               // internal
		false,               // no-wait
		nil,                 // arguments
	)
	if err != nil {
		channel.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	logger.Info("Connected to RabbitMQ",
		zap.String("host", config.Host),
		zap.Int("port", config.Port),
		zap.String("exchange", config.Exchange),
		zap.String("exchange_type", config.ExchangeType),
	)

	return &RabbitMQClient{
		conn:    conn,
		channel: channel,
		config:  config,
	}, nil
}

// Publish publishes a message to a queue
func (c *RabbitMQClient) Publish(ctx context.Context, queueName string, body []byte) error {
	start := time.Now()

	// Declare queue
	_, err := c.channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		c.logOperation(ctx, "publish_declare_queue", queueName, start, err)
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	// Bind queue to exchange
	err = c.channel.QueueBind(
		queueName,         // queue name
		queueName,         // routing key
		c.config.Exchange, // exchange
		false,
		nil,
	)
	if err != nil {
		c.logOperation(ctx, "publish_bind_queue", queueName, start, err)
		return fmt.Errorf("failed to bind queue: %w", err)
	}

	headers := amqp.Table{}
	if meta, ok := midware.RequestMetadataFromContext(ctx); ok {
		setHeaderIfPresent(headers, midware.HeaderRequestID, meta.RequestID)
		setHeaderIfPresent(headers, midware.HeaderTraceID, meta.TraceID)
		setHeaderIfPresent(headers, midware.HeaderUserID, meta.UserID)
		setHeaderIfPresent(headers, midware.HeaderTenantID, meta.TenantID)
		if auth := meta.ForwardHeaders[midware.HeaderAuth]; auth != "" {
			headers[midware.HeaderAuth] = auth
		}
	}

	// Publish message
	err = c.channel.PublishWithContext(
		ctx,
		c.config.Exchange, // exchange
		queueName,         // routing key
		false,             // mandatory
		false,             // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
			Headers:      headers,
		},
	)
	if err != nil {
		c.logOperation(ctx, "publish", queueName, start, err)
		return fmt.Errorf("failed to publish message: %w", err)
	}

	c.logOperation(ctx, "publish", queueName, start, nil)
	return nil
}

// Consume consumes messages from a queue
func (c *RabbitMQClient) Consume(queueName string) (<-chan amqp.Delivery, error) {
	start := time.Now()
	// Declare queue
	_, err := c.channel.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		c.logOperation(context.Background(), "consume_declare_queue", queueName, start, err)
		return nil, fmt.Errorf("failed to declare queue: %w", err)
	}

	// Bind queue to exchange
	err = c.channel.QueueBind(
		queueName,         // queue name
		queueName,         // routing key
		c.config.Exchange, // exchange
		false,
		nil,
	)
	if err != nil {
		c.logOperation(context.Background(), "consume_bind_queue", queueName, start, err)
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	// Start consuming
	msgs, err := c.channel.Consume(
		queueName, // queue
		"",        // consumer
		false,     // auto-ack
		false,     // exclusive
		false,     // no-local
		false,     // no-wait
		nil,       // args
	)
	if err != nil {
		c.logOperation(context.Background(), "consume", queueName, start, err)
		return nil, fmt.Errorf("failed to consume messages: %w", err)
	}

	c.logOperation(context.Background(), "consume", queueName, start, nil)
	return msgs, nil
}

// Close closes the RabbitMQ connection
func (c *RabbitMQClient) Close() error {
	logger.Info("Closing RabbitMQ client")
	if c.channel != nil {
		if err := c.channel.Close(); err != nil {
			return err
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return err
		}
	}
	return nil
}

// IsConnected checks if the client is connected
func (c *RabbitMQClient) IsConnected() bool {
	return c.conn != nil && !c.conn.IsClosed()
}

func (c *RabbitMQClient) logOperation(ctx context.Context, operation string, queueName string, start time.Time, err error) {
	fields := []zap.Field{
		zap.String("component", "rabbitmq"),
		zap.String("operation", operation),
		zap.String("queue", queueName),
		zap.String("exchange", c.config.Exchange),
		zap.Duration("latency", time.Since(start)),
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
		fields = append(fields, zap.Error(err))
		logger.Warn("RabbitMQ operation failed", fields...)
		return
	}

	logger.Debug("RabbitMQ operation completed", fields...)
}

func setHeaderIfPresent(headers amqp.Table, key, value string) {
	if value != "" {
		headers[key] = value
	}
}
