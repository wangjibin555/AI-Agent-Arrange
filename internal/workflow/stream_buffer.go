package workflow

import (
	"sync"
	"time"
)

// StreamChunk 表示一个流式数据块
type StreamChunk struct {
	Index     int                    `json:"index"`     // 块索引
	Data      map[string]interface{} `json:"data"`      // 数据内容
	Tokens    int                    `json:"tokens"`    // token数量
	Timestamp time.Time              `json:"timestamp"` // 时间戳
}

// StreamSubscriber 表示流式数据的订阅者
type StreamSubscriber struct {
	StepID    string           // 订阅者步骤ID
	Channel   chan StreamChunk // 数据通道
	MinTokens int              // 最小启动token数
	Started   bool             // 是否已启动
	lastIndex int              // 最后处理的chunk索引
	mu        sync.Mutex
}

// StreamBuffer 管理步骤间的流式数据传递
type StreamBuffer struct {
	stepID        string
	chunks        []StreamChunk
	subscribers   []*StreamSubscriber
	totalTokens   int
	completed     bool
	lastChunkTime time.Time
	mu            sync.RWMutex
	config        *StreamingConfig
}

// NewStreamBuffer 创建新的流式缓冲区
func NewStreamBuffer(stepID string, config *StreamingConfig) *StreamBuffer {
	if config == nil {
		config = &StreamingConfig{
			Enabled:        true,
			BufferStrategy: DefaultBufferStrategy,
			BufferSize:     DefaultBufferSize,
			StreamTimeout:  DefaultStreamTimeout,
		}
	}

	return &StreamBuffer{
		stepID:        stepID,
		chunks:        make([]StreamChunk, 0),
		subscribers:   make([]*StreamSubscriber, 0),
		totalTokens:   0,
		completed:     false,
		lastChunkTime: time.Now(),
		config:        config,
	}
}

// Write 写入一个数据块到缓冲区
func (b *StreamBuffer) Write(chunk StreamChunk) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 添加到缓冲区
	b.chunks = append(b.chunks, chunk)
	b.totalTokens += chunk.Tokens
	b.lastChunkTime = time.Now()

	// 通知订阅者
	b.notifySubscribersLocked()

	return nil
}

// Subscribe 订阅流式数据
func (b *StreamBuffer) Subscribe(stepID string, minTokens int) *StreamSubscriber {
	b.mu.Lock()
	defer b.mu.Unlock()

	subscriber := &StreamSubscriber{
		StepID:    stepID,
		Channel:   make(chan StreamChunk, 100), // 缓冲通道
		MinTokens: minTokens,
		Started:   false,
		lastIndex: -1,
	}

	b.subscribers = append(b.subscribers, subscriber)

	// 如果已有数据，立即发送
	if len(b.chunks) > 0 {
		go b.sendExistingChunks(subscriber)
	}

	return subscriber
}

// sendExistingChunks 发送已存在的数据块给新订阅者
func (b *StreamBuffer) sendExistingChunks(subscriber *StreamSubscriber) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for i, chunk := range b.chunks {
		if i > subscriber.lastIndex {
			select {
			case subscriber.Channel <- chunk:
				subscriber.mu.Lock()
				subscriber.lastIndex = i
				subscriber.mu.Unlock()
			default:
				// 通道满，跳过
			}
		}
	}

	// 检查是否应该启动订阅者
	b.checkSubscriberStartLocked(subscriber)
}

// notifySubscribersLocked 通知所有订阅者（需要持有锁）
func (b *StreamBuffer) notifySubscribersLocked() {
	if len(b.subscribers) == 0 {
		return
	}

	// 获取最新的chunk
	latestChunk := b.chunks[len(b.chunks)-1]

	for _, subscriber := range b.subscribers {
		// 只发送新数据
		if latestChunk.Index > subscriber.lastIndex {
			select {
			case subscriber.Channel <- latestChunk:
				subscriber.mu.Lock()
				subscriber.lastIndex = latestChunk.Index
				subscriber.mu.Unlock()

				// 检查是否达到启动条件
				b.checkSubscriberStartLocked(subscriber)
			default:
				// 通道满，跳过此订阅者
			}
		}
	}
}

// checkSubscriberStartLocked 检查订阅者是否应该启动（需要持有读锁）
func (b *StreamBuffer) checkSubscriberStartLocked(subscriber *StreamSubscriber) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()

	if !subscriber.Started && b.totalTokens >= subscriber.MinTokens {
		subscriber.Started = true
	}
}

// MarkComplete 标记流式数据传输完成
func (b *StreamBuffer) MarkComplete() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.completed {
		return
	}

	b.completed = true

	// 关闭所有订阅者通道
	for _, subscriber := range b.subscribers {
		close(subscriber.Channel)
	}
}

// IsCompleted 检查是否已完成
func (b *StreamBuffer) IsCompleted() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.completed
}

// TotalTokens 返回总token数
func (b *StreamBuffer) TotalTokens() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalTokens
}

// TotalChunks 返回总块数
func (b *StreamBuffer) TotalChunks() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.chunks)
}

// ReadAll 读取所有数据块
func (b *StreamBuffer) ReadAll() []StreamChunk {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// 返回副本，避免外部修改
	result := make([]StreamChunk, len(b.chunks))
	copy(result, b.chunks)
	return result
}

// ReadRange 读取指定范围的数据块
func (b *StreamBuffer) ReadRange(start, end int) []StreamChunk {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if start < 0 {
		start = 0
	}
	if end > len(b.chunks) {
		end = len(b.chunks)
	}
	if start >= end {
		return []StreamChunk{}
	}

	result := make([]StreamChunk, end-start)
	copy(result, b.chunks[start:end])
	return result
}

// TimeSinceLastChunk 返回距离最后一个chunk的时间
func (b *StreamBuffer) TimeSinceLastChunk() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return time.Since(b.lastChunkTime)
}

// GetConfig 返回配置
func (b *StreamBuffer) GetConfig() *StreamingConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config
}

// Size 返回缓冲区当前大小（估算）
func (b *StreamBuffer) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// 简单估算：每个chunk约1KB
	return len(b.chunks) * 1024
}

// TruncateAfter 截断指定索引之后的数据（保留<=index的chunk）
func (b *StreamBuffer) TruncateAfter(index int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if index < 0 {
		b.chunks = make([]StreamChunk, 0)
		b.totalTokens = 0
		return
	}
	if index >= len(b.chunks)-1 {
		return
	}

	truncated := b.chunks[:index+1]
	totalTokens := 0
	for _, chunk := range truncated {
		totalTokens += chunk.Tokens
	}

	b.chunks = append([]StreamChunk(nil), truncated...)
	b.totalTokens = totalTokens
	if len(b.chunks) > 0 {
		b.lastChunkTime = b.chunks[len(b.chunks)-1].Timestamp
	}
}

// GetSubscriberCount 返回订阅者数量
func (b *StreamBuffer) GetSubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Clear 清空缓冲区（慎用）
func (b *StreamBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.chunks = make([]StreamChunk, 0)
	b.totalTokens = 0
}

// Unsubscribe 取消订阅
func (b *StreamBuffer) Unsubscribe(stepID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, subscriber := range b.subscribers {
		if subscriber.StepID == stepID {
			close(subscriber.Channel)
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			break
		}
	}
}
