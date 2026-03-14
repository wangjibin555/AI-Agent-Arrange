package messaging

import (
	"context"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
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

	return &RabbitMQClient{
		conn:    conn,
		channel: channel,
		config:  config,
	}, nil
}

// Publish publishes a message to a queue
func (c *RabbitMQClient) Publish(ctx context.Context, queueName string, body []byte) error {
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
		return fmt.Errorf("failed to bind queue: %w", err)
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
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// Consume consumes messages from a queue
func (c *RabbitMQClient) Consume(queueName string) (<-chan amqp.Delivery, error) {
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
		return nil, fmt.Errorf("failed to consume messages: %w", err)
	}

	return msgs, nil
}

// Close closes the RabbitMQ connection
func (c *RabbitMQClient) Close() error {
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
