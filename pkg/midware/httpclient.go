package midware

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/wangjibin555/AI-Agent-Arrange/pkg/logger"
	"go.uber.org/zap"
)

// HTTPClientConfig defines outbound network defaults for dependency calls.
type HTTPClientConfig struct {
	Timeout         time.Duration
	RetryMax        int
	RetryWait       time.Duration
	UserAgent       string
	MaxIdleConns    int
	MaxConnsPerHost int
	IdleConnTimeout time.Duration
}

// HTTPClient wraps net/http with request metadata propagation and access logs.
type HTTPClient struct {
	client *http.Client
	config HTTPClientConfig
}

func DefaultHTTPClientConfig() HTTPClientConfig {
	return HTTPClientConfig{
		Timeout:         15 * time.Second,
		RetryMax:        2,
		RetryWait:       200 * time.Millisecond,
		UserAgent:       "ai-agent-arrange/0.1",
		MaxIdleConns:    100,
		MaxConnsPerHost: 20,
		IdleConnTimeout: 90 * time.Second,
	}
}

func NewHTTPClient(config HTTPClientConfig) *HTTPClient {
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	if config.RetryWait <= 0 {
		config.RetryWait = 200 * time.Millisecond
	}
	if config.UserAgent == "" {
		config.UserAgent = "ai-agent-arrange/0.1"
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 100
	}
	if config.MaxConnsPerHost <= 0 {
		config.MaxConnsPerHost = 20
	}
	if config.IdleConnTimeout <= 0 {
		config.IdleConnTimeout = 90 * time.Second
	}

	transport := &http.Transport{
		MaxIdleConns:    config.MaxIdleConns,
		MaxConnsPerHost: config.MaxConnsPerHost,
		IdleConnTimeout: config.IdleConnTimeout,
	}

	return &HTTPClient{
		config: config,
		client: &http.Client{
			Timeout:   config.Timeout,
			Transport: transport,
		},
	}
}

func (c *HTTPClient) Raw() *http.Client {
	return c.client
}

func (c *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	attempt := 0
	for {
		attempt++

		clonedReq, err := cloneRequest(req)
		if err != nil {
			return nil, err
		}

		if clonedReq.Header.Get("User-Agent") == "" && c.config.UserAgent != "" {
			clonedReq.Header.Set("User-Agent", c.config.UserAgent)
		}
		InjectForwardHeaders(clonedReq)

		start := time.Now()
		resp, err := c.client.Do(clonedReq)
		duration := time.Since(start)
		c.logRequest(clonedReq, resp, err, attempt, duration)

		if !c.shouldRetry(resp, err, attempt) {
			return resp, err
		}

		if resp != nil && resp.Body != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		time.Sleep(c.config.RetryWait)
	}
}

func (c *HTTPClient) shouldRetry(resp *http.Response, err error, attempt int) bool {
	if attempt > c.config.RetryMax {
		return false
	}

	if err != nil {
		var netErr net.Error
		return errors.As(err, &netErr)
	}

	if resp == nil {
		return false
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (c *HTTPClient) logRequest(req *http.Request, resp *http.Response, err error, attempt int, duration time.Duration) {
	meta, _ := RequestMetadataFromRequest(req)

	fields := []zap.Field{
		zap.String("method", req.Method),
		zap.String("url", req.URL.String()),
		zap.Int("attempt", attempt),
		zap.Duration("latency", duration),
	}

	if meta.RequestID != "" {
		fields = append(fields, zap.String("request_id", meta.RequestID))
	}
	if meta.TraceID != "" {
		fields = append(fields, zap.String("trace_id", meta.TraceID))
	}
	if resp != nil {
		fields = append(fields, zap.Int("status", resp.StatusCode))
	}

	if err != nil {
		fields = append(fields, zap.Error(err))
		logger.Warn("Outbound HTTP request failed", fields...)
		return
	}

	logger.Info("Outbound HTTP request", fields...)
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	cloned := req.Clone(req.Context())
	if req.Body == nil {
		return cloned, nil
	}
	if req.GetBody == nil {
		return nil, fmt.Errorf("request body is not replayable; set GetBody before using retryable client")
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("failed to clone request body: %w", err)
	}
	cloned.Body = body
	return cloned, nil
}
