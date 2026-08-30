package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	defaultBatchImageWorkerLockTTL             = 5 * time.Minute
	defaultBatchImageWorkerLockConflictDelay   = 5 * time.Second
	defaultBatchImageWorkerErrorRetryDelay     = time.Minute
	defaultBatchImageWorkerRequeueDelay        = 30 * time.Second
	defaultBatchImageWorkerDelayedPollInterval = 5 * time.Second
	defaultBatchImageWorkerRecoveryInterval    = 5 * time.Minute
	defaultBatchImageWorkerStaleActiveAfter    = 10 * time.Minute
	defaultBatchImageWorkerDelayedMoveLimit    = 100
	defaultBatchImageWorkerRecoverLimit        = 100
	defaultBatchImageWorkerErrorBackoff        = time.Second
	defaultBatchImageWorkerReserveBlockTimeout = 5 * time.Second
	defaultBatchImageWorkerConcurrency         = 4
	defaultBatchImageWorkerProcessTimeout      = 15 * time.Minute
	defaultBatchImageWorkerMaxProcessFailures  = 5
)

type BatchImageProcessor interface {
	Process(ctx context.Context, batchID string) (BatchImageProcessResult, error)
}

type BatchImageProcessResult struct {
	RequeueAfter time.Duration
	Terminal     bool
}

type BatchImageWorkerOptions struct {
	ReserveBlockTimeout time.Duration
	JobLockTTL          time.Duration
	LockConflictDelay   time.Duration
	DefaultRequeueDelay time.Duration
	ErrorRetryDelay     time.Duration
	ErrorBackoff        time.Duration
	DelayedPollInterval time.Duration
	RecoveryInterval    time.Duration
	StaleActiveAfter    time.Duration
	DelayedMoveLimit    int
	RecoverLimit        int
	// Concurrency 是单实例消费协程数；同一 job 仍由队列锁串行。
	Concurrency int
	// ProcessTimeout 单次 Process 硬超时，防止长任务拖垮全局吞吐。
	ProcessTimeout time.Duration
	// MaxProcessFailures 连续处理失败上限，达到后 Ack 死信（依赖 processor/settlement 终态或 DB 失败计数）。
	MaxProcessFailures int
}

type BatchImageWorker struct {
	queue     BatchImageQueue
	processor BatchImageProcessor
	opts      BatchImageWorkerOptions
	// processFailures 跟踪 batchID 连续失败次数（进程内）；用于毒消息死信出口。
	processFailures map[string]int
	failuresMu      sync.Mutex
}

func NewBatchImageWorker(queue BatchImageQueue, processor BatchImageProcessor, opts BatchImageWorkerOptions) *BatchImageWorker {
	return &BatchImageWorker{
		queue:           queue,
		processor:       processor,
		opts:            normalizeBatchImageWorkerOptions(opts),
		processFailures: make(map[string]int),
	}
}

func NewBatchImageWorkerOptionsFromConfig(cfg *config.Config) BatchImageWorkerOptions {
	if cfg == nil {
		return normalizeBatchImageWorkerOptions(BatchImageWorkerOptions{})
	}
	return normalizeBatchImageWorkerOptions(BatchImageWorkerOptions{
		JobLockTTL:          time.Duration(cfg.BatchImage.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.BatchImage.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.BatchImage.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.BatchImage.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.BatchImage.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.BatchImage.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.BatchImage.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.BatchImage.DelayedMoveLimit,
		RecoverLimit:        cfg.BatchImage.RecoverLimit,
	})
}

func normalizeBatchImageWorkerOptions(opts BatchImageWorkerOptions) BatchImageWorkerOptions {
	if opts.ReserveBlockTimeout <= 0 {
		opts.ReserveBlockTimeout = defaultBatchImageWorkerReserveBlockTimeout
	}
	if opts.JobLockTTL <= 0 {
		opts.JobLockTTL = defaultBatchImageWorkerLockTTL
	}
	if opts.LockConflictDelay <= 0 {
		opts.LockConflictDelay = defaultBatchImageWorkerLockConflictDelay
	}
	if opts.DefaultRequeueDelay <= 0 {
		opts.DefaultRequeueDelay = defaultBatchImageWorkerRequeueDelay
	}
	if opts.ErrorRetryDelay <= 0 {
		opts.ErrorRetryDelay = defaultBatchImageWorkerErrorRetryDelay
	}
	if opts.ErrorBackoff <= 0 {
		opts.ErrorBackoff = defaultBatchImageWorkerErrorBackoff
	}
	if opts.DelayedPollInterval <= 0 {
		opts.DelayedPollInterval = defaultBatchImageWorkerDelayedPollInterval
	}
	if opts.RecoveryInterval <= 0 {
		opts.RecoveryInterval = defaultBatchImageWorkerRecoveryInterval
	}
	if opts.StaleActiveAfter <= 0 {
		opts.StaleActiveAfter = defaultBatchImageWorkerStaleActiveAfter
	}
	if opts.DelayedMoveLimit <= 0 {
		opts.DelayedMoveLimit = defaultBatchImageWorkerDelayedMoveLimit
	}
	if opts.RecoverLimit <= 0 {
		opts.RecoverLimit = defaultBatchImageWorkerRecoverLimit
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultBatchImageWorkerConcurrency
	}
	if opts.ProcessTimeout <= 0 {
		opts.ProcessTimeout = defaultBatchImageWorkerProcessTimeout
	}
	if opts.MaxProcessFailures <= 0 {
		opts.MaxProcessFailures = defaultBatchImageWorkerMaxProcessFailures
	}
	return opts
}

func (w *BatchImageWorker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	n := w.opts.Concurrency
	if n <= 1 {
		w.runLoop(ctx)
		return
	}
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w.runLoop(ctx)
		}()
	}
	wg.Wait()
}

func (w *BatchImageWorker) runLoop(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			sleepOrDone(ctx, w.opts.ErrorBackoff)
		}
	}
}

func (w *BatchImageWorker) RunOnce(ctx context.Context) error {
	if w == nil || w.queue == nil || w.processor == nil {
		return nil
	}

	reserved, err := w.queue.Reserve(ctx, w.opts.ReserveBlockTimeout)
	if errors.Is(err, ErrBatchImageQueueEmpty) {
		return nil
	}
	if err != nil {
		return err
	}

	lock, ok, err := w.queue.TryAcquireJobLock(ctx, reserved.BatchID, w.opts.JobLockTTL)
	if err != nil {
		if requeueErr := w.queue.RequeueAfter(ctx, reserved.BatchID, w.opts.LockConflictDelay); requeueErr != nil {
			return requeueErr
		}
		return err
	}
	if !ok {
		// 锁被其他实例持有：按冲突延迟重新入队。直接丢弃会让 job 滞留在
		// active zset，最早要等 StaleActiveAfter 才被恢复，造成分钟级停摆。
		return w.queue.RequeueAfter(ctx, reserved.BatchID, w.opts.LockConflictDelay)
	}
	defer func() {
		_ = lock.Release(ctx)
	}()

	// 处理期间持续心跳：刷新 active zset 时间戳防止 stale 恢复把在处理的
	// job 重投给其他 worker，并对支持续期的锁实现延长锁 TTL。
	hbStop := make(chan struct{})
	hbDone := make(chan struct{})
	go w.runJobHeartbeat(ctx, reserved.BatchID, lock, hbStop, hbDone)

	processCtx := ctx
	var cancelProcess context.CancelFunc
	if w.opts.ProcessTimeout > 0 {
		processCtx, cancelProcess = context.WithTimeout(ctx, w.opts.ProcessTimeout)
		defer cancelProcess()
	}
	result, err := w.processor.Process(processCtx, reserved.BatchID)
	close(hbStop)
	<-hbDone
	if err != nil {
		logger.L().Warn("batch_image.worker_process_failed",
			zap.String("batch_id", reserved.BatchID),
			zap.Error(err),
		)
		if w.noteProcessFailure(reserved.BatchID) >= w.opts.MaxProcessFailures {
			logger.L().Error("batch_image.worker_dead_letter",
				zap.String("batch_id", reserved.BatchID),
				zap.Int("failures", w.opts.MaxProcessFailures),
				zap.Error(err),
			)
			w.clearProcessFailure(reserved.BatchID)
			// 毒消息/永久失败：Ack 出队，避免拖垮全局。业务侧 hold 释放依赖
			// settlement/processor 终态或 billing recovery 扫描。
			return w.queue.Ack(ctx, reserved.BatchID)
		}
		return w.queue.RequeueAfter(ctx, reserved.BatchID, w.opts.ErrorRetryDelay)
	}
	w.clearProcessFailure(reserved.BatchID)
	if result.Terminal {
		return w.queue.Ack(ctx, reserved.BatchID)
	}
	delay := result.RequeueAfter
	if delay <= 0 {
		delay = w.opts.DefaultRequeueDelay
	}
	return w.queue.RequeueAfter(ctx, reserved.BatchID, delay)
}

func (w *BatchImageWorker) noteProcessFailure(batchID string) int {
	if w == nil {
		return 0
	}
	w.failuresMu.Lock()
	defer w.failuresMu.Unlock()
	if w.processFailures == nil {
		w.processFailures = make(map[string]int)
	}
	w.processFailures[batchID]++
	return w.processFailures[batchID]
}

func (w *BatchImageWorker) clearProcessFailure(batchID string) {
	if w == nil {
		return
	}
	w.failuresMu.Lock()
	defer w.failuresMu.Unlock()
	delete(w.processFailures, batchID)
}

// BatchImageJobLockRefresher 是可选的锁续期能力；由具体锁实现按需提供。
type BatchImageJobLockRefresher interface {
	Refresh(ctx context.Context, ttl time.Duration) error
}

func (w *BatchImageWorker) heartbeatInterval() time.Duration {
	interval := w.opts.JobLockTTL
	if w.opts.StaleActiveAfter < interval {
		interval = w.opts.StaleActiveAfter
	}
	interval /= 3
	if interval < time.Second {
		interval = time.Second
	}
	return interval
}

func (w *BatchImageWorker) runJobHeartbeat(ctx context.Context, batchID string, lock BatchImageJobLock, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(w.heartbeatInterval())
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.queue.Heartbeat(ctx, batchID); err != nil && ctx.Err() == nil {
				logger.L().Warn("batch_image.worker_heartbeat_failed",
					zap.String("batch_id", batchID),
					zap.Error(err),
				)
			}
			if refresher, ok := lock.(BatchImageJobLockRefresher); ok {
				if err := refresher.Refresh(ctx, w.opts.JobLockTTL); err != nil && ctx.Err() == nil {
					logger.L().Warn("batch_image.worker_lock_refresh_failed",
						zap.String("batch_id", batchID),
						zap.Error(err),
					)
				}
			}
		}
	}
}

func (w *BatchImageWorker) MoveDueDelayedOnce(ctx context.Context) (int, error) {
	if w == nil || w.queue == nil {
		return 0, nil
	}
	return w.queue.MoveDueDelayedToReady(ctx, w.opts.DelayedMoveLimit)
}

func (w *BatchImageWorker) RunDelayedMover(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		moved, _ := w.MoveDueDelayedOnce(ctx)
		if moved > 0 {
			continue
		}
		sleepOrDone(ctx, w.opts.DelayedPollInterval)
	}
}

func (w *BatchImageWorker) RecoverStaleActiveOnce(ctx context.Context) (int, error) {
	if w == nil || w.queue == nil {
		return 0, nil
	}
	return w.queue.RecoverStaleActive(ctx, w.opts.StaleActiveAfter, w.opts.RecoverLimit)
}

func (w *BatchImageWorker) RunStaleActiveRecovery(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		_, _ = w.RecoverStaleActiveOnce(ctx)
		sleepOrDone(ctx, w.opts.RecoveryInterval)
	}
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
