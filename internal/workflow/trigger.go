package workflow

import (
	"fmt"
	"sync"
	"time"
)

// Trigger 智能触发器，用于判断步骤是否应该启动
type Trigger struct {
	stepID      string
	step        *Step
	buffer      *StreamBuffer            // 保留用于单依赖兼容
	buffers     map[string]*StreamBuffer // 多依赖buffer: depID -> buffer
	triggered   bool
	triggerTime *time.Time
	mu          sync.Mutex
}

// NewTrigger 创建新的触发器（单依赖兼容）
func NewTrigger(stepID string, step *Step, buffer *StreamBuffer) *Trigger {
	return &Trigger{
		stepID:    stepID,
		step:      step,
		buffer:    buffer,
		buffers:   make(map[string]*StreamBuffer),
		triggered: false,
	}
}

// NewMultiTrigger 创建多依赖触发器
func NewMultiTrigger(stepID string, step *Step, buffers map[string]*StreamBuffer) *Trigger {
	return &Trigger{
		stepID:    stepID,
		step:      step,
		buffers:   buffers,
		triggered: false,
	}
}

// ShouldTrigger 检查是否应该触发步骤执行
// 返回: (shouldTrigger, reason)
func (t *Trigger) ShouldTrigger() (bool, string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 如果已经触发过，不再触发
	if t.triggered {
		return false, "already_triggered"
	}

	// 如果步骤未启用流式，立即触发
	if t.step.Streaming == nil || !t.step.Streaming.Enabled {
		return true, "not_streaming"
	}

	// 获取有效的buffers
	activeBuffers := t.getActiveBuffers()

	// 如果步骤定义了依赖，但buffer还未准备好，需要等待
	if len(t.step.DependsOn) > 0 && len(activeBuffers) == 0 {
		return false, "waiting_for_buffers"
	}

	// 如果没有上游buffer（根节点），立即触发
	if len(activeBuffers) == 0 {
		return true, "no_upstream"
	}

	// 如果等待完整数据，检查所有上游是否完成
	if t.step.Streaming.WaitFor == "full" {
		allCompleted := true
		for _, buffer := range activeBuffers {
			if buffer == nil || !buffer.IsCompleted() {
				allCompleted = false
				break
			}
		}
		if allCompleted {
			return true, "all_upstream_completed"
		}
		return false, "waiting_for_full_data"
	}

	// 如果等待部分数据 (wait_for: "partial")
	if t.step.Streaming.WaitFor == "partial" {
		minTokens := t.step.Streaming.MinStartTokens

		// 检查所有依赖是否满足条件
		allReady := true
		readyCount := 0
		totalDeps := len(activeBuffers)

		for depID, buffer := range activeBuffers {
			if buffer == nil {
				allReady = false
				continue
			}

			// 检查这个依赖是否满足条件
			depReady := false
			if minTokens > 0 {
				// 条件1: 达到MinStartTokens阈值
				if buffer.TotalTokens() >= minTokens {
					depReady = true
				}
			}

			// 条件2: 上游已完成（即使未达到阈值也认为ready）
			if buffer.IsCompleted() {
				depReady = true
			}

			// 条件3: 流式超时（上游长时间无数据）
			if t.step.Streaming.StreamTimeout > 0 {
				timeout := time.Duration(t.step.Streaming.StreamTimeout) * time.Second
				if buffer.TimeSinceLastChunk() > timeout {
					depReady = true
				}
			}

			if depReady {
				readyCount++
			} else {
				allReady = false
			}

			_ = depID // 使用depID避免编译警告
		}

		// 所有依赖都满足才触发
		if allReady && readyCount == totalDeps {
			return true, fmt.Sprintf("all_deps_ready_%d/%d", readyCount, totalDeps)
		}

		return false, fmt.Sprintf("waiting_deps_%d/%d", readyCount, totalDeps)
	}

	// 默认立即触发
	return true, "default"
}

// getActiveBuffers 获取有效的buffer列表（优先使用buffers，fallback到buffer）
func (t *Trigger) getActiveBuffers() map[string]*StreamBuffer {
	if len(t.buffers) > 0 {
		return t.buffers
	}
	// 兼容旧的单buffer模式
	if t.buffer != nil {
		return map[string]*StreamBuffer{"default": t.buffer}
	}
	return make(map[string]*StreamBuffer)
}

// MarkTriggered 标记为已触发
func (t *Trigger) MarkTriggered() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.triggered {
		t.triggered = true
		now := time.Now()
		t.triggerTime = &now
	}
}

// IsTriggered 检查是否已触发
func (t *Trigger) IsTriggered() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.triggered
}

// GetTriggerTime 获取触发时间
func (t *Trigger) GetTriggerTime() *time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.triggerTime
}

// UpdateBuffers 动态更新依赖的buffers
func (t *Trigger) UpdateBuffers(buffers map[string]*StreamBuffer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buffers = buffers
}

// UpdateBuffer 更新单个buffer（兼容方法）
func (t *Trigger) UpdateBuffer(buffer *StreamBuffer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buffer = buffer
}

// TriggerManager 管理多个触发器
type TriggerManager struct {
	triggers map[string]*Trigger // stepID -> trigger
	mu       sync.RWMutex
}

// NewTriggerManager 创建触发器管理器
func NewTriggerManager() *TriggerManager {
	return &TriggerManager{
		triggers: make(map[string]*Trigger),
	}
}

// RegisterTrigger 注册触发器
func (m *TriggerManager) RegisterTrigger(stepID string, trigger *Trigger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.triggers[stepID] = trigger
}

// GetTrigger 获取触发器
func (m *TriggerManager) GetTrigger(stepID string) *Trigger {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.triggers[stepID]
}

// CheckTriggers 检查所有触发器，返回应该触发的步骤ID列表
func (m *TriggerManager) CheckTriggers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var triggered []string
	for stepID, trigger := range m.triggers {
		if shouldTrigger, _ := trigger.ShouldTrigger(); shouldTrigger {
			triggered = append(triggered, stepID)
		}
	}

	return triggered
}

// RemoveTrigger 移除触发器
func (m *TriggerManager) RemoveTrigger(stepID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.triggers, stepID)
}

// Clear 清空所有触发器
func (m *TriggerManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.triggers = make(map[string]*Trigger)
}

// GetPendingCount 获取待触发的触发器数量
func (m *TriggerManager) GetPendingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, trigger := range m.triggers {
		if !trigger.IsTriggered() {
			count++
		}
	}
	return count
}

// ChunkTriggerMonitor 监控ChunkSize触发
type ChunkTriggerMonitor struct {
	stepID          string
	step            *Step
	buffer          *StreamBuffer
	lastTriggeredAt int    // 上次触发时的token数
	triggerCallback func() // 触发回调
	mu              sync.Mutex
}

// NewChunkTriggerMonitor 创建ChunkSize监控器
func NewChunkTriggerMonitor(stepID string, step *Step, buffer *StreamBuffer, callback func()) *ChunkTriggerMonitor {
	return &ChunkTriggerMonitor{
		stepID:          stepID,
		step:            step,
		buffer:          buffer,
		lastTriggeredAt: 0,
		triggerCallback: callback,
	}
}

// Check 检查是否应该触发
func (m *ChunkTriggerMonitor) Check() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果未配置ChunkSize或TriggerNext，不触发
	if m.step.Streaming == nil || m.step.Streaming.ChunkSize <= 0 || !m.step.Streaming.TriggerNext {
		return false
	}

	// 检查当前token数
	currentTokens := m.buffer.TotalTokens()
	chunkSize := m.step.Streaming.ChunkSize

	// 如果达到ChunkSize的倍数，触发
	if currentTokens >= m.lastTriggeredAt+chunkSize {
		m.lastTriggeredAt = currentTokens
		if m.triggerCallback != nil {
			go m.triggerCallback()
		}
		return true
	}

	return false
}

// Reset 重置监控器
func (m *ChunkTriggerMonitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTriggeredAt = 0
}
