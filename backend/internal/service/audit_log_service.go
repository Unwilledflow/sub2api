package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	auditLogQueueCapacity = 4096
	auditLogBatchSize     = 100
	auditLogFlushInterval = time.Second

	auditRetentionCheckInterval = 24 * time.Hour
	auditRetentionStartupDelay  = 5 * time.Minute
	auditRetentionBatchSize     = 5000
)

// AuditLogService 管理面操作审计日志服务。
// 写入端为非阻塞异步批量落库（不拖慢管理请求）；
// 读取端提供分页查询；清空端点由 handler 层做 TOTP 强校验后调用 ClearAll。
type AuditLogService struct {
	repo           AuditLogRepository
	settingService *SettingService

	queue chan *AuditLog

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	droppedCount uint64
	writeFailed  uint64
	writtenCount uint64
}

func NewAuditLogService(repo AuditLogRepository, settingService *SettingService) *AuditLogService {
	ctx, cancel := context.WithCancel(context.Background())
	return &AuditLogService{
		repo:           repo,
		settingService: settingService,
		queue:          make(chan *AuditLog, auditLogQueueCapacity),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start 启动异步写入与保留期清理协程。
func (s *AuditLogService) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.wg.Add(2)
	go s.runWriter()
	go s.runRetentionLoop()
}

// Stop 停止服务并尽量落盘队列中剩余记录。
func (s *AuditLogService) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

// AuditLogWriteStats exposes async writer health for ops/runtime surfaces.
type AuditLogWriteStats struct {
	Dropped     uint64 `json:"dropped"`
	WriteFailed uint64 `json:"write_failed"`
	Written     uint64 `json:"written"`
	QueueLen    int    `json:"queue_len"`
	QueueCap    int    `json:"queue_cap"`
}

// Stats returns drop/write-failure counters so silent audit loss is observable.
func (s *AuditLogService) Stats() AuditLogWriteStats {
	if s == nil {
		return AuditLogWriteStats{}
	}
	return AuditLogWriteStats{
		Dropped:     atomic.LoadUint64(&s.droppedCount),
		WriteFailed: atomic.LoadUint64(&s.writeFailed),
		Written:     atomic.LoadUint64(&s.writtenCount),
		QueueLen:    len(s.queue),
		QueueCap:    cap(s.queue),
	}
}

// Record 非阻塞入队一条审计记录；队列打满时丢弃并计数（管理面流量下几乎不可能发生）。
func (s *AuditLogService) Record(entry *AuditLog) {
	if s == nil || entry == nil {
		return
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	select {
	case <-s.ctx.Done():
		atomic.AddUint64(&s.droppedCount, 1)
		s.alertDrop("context_done")
		return
	default:
	}
	select {
	case s.queue <- entry:
	default:
		n := atomic.AddUint64(&s.droppedCount, 1)
		s.alertDrop("queue_full")
		if n == 1 || n%100 == 0 {
			_, _ = fmt.Fprintf(os.Stderr, "time=%s level=ERROR msg=\"audit log queue drop\" dropped_total=%d reason=queue_full\n",
				time.Now().Format(time.RFC3339Nano), n)
		}
	}
}

func (s *AuditLogService) alertDrop(reason string) {
	// Best-effort stderr alert; counters are exposed via Stats().
	_ = reason
}

// List 分页查询审计日志。
func (s *AuditLogService) List(ctx context.Context, filter *AuditLogFilter) (*AuditLogList, error) {
	return s.repo.List(ctx, filter)
}

// GetByID 查询单条详情。
func (s *AuditLogService) GetByID(ctx context.Context, id int64) (*AuditLog, error) {
	return s.repo.GetByID(ctx, id)
}

// ClearAll 全量清空审计日志并写入留痕记录。
// 调用方（handler）必须先完成 TOTP 验证；本方法负责：
//  1. 统计并清空全表
//  2. 同步写入一条 "audit_log.clear" 留痕记录（绕过异步队列，保证落库）
func (s *AuditLogService) ClearAll(ctx context.Context, trace *AuditLog) (int64, error) {
	deleted, err := s.repo.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count audit logs: %w", err)
	}
	if err := s.repo.TruncateAll(ctx); err != nil {
		return 0, fmt.Errorf("truncate audit logs: %w", err)
	}

	if trace != nil {
		trace.Action = AuditActionAuditLogClear
		if trace.CreatedAt.IsZero() {
			trace.CreatedAt = time.Now().UTC()
		}
		if trace.Extra == nil {
			trace.Extra = map[string]any{}
		}
		trace.Extra["deleted_rows"] = deleted
		if err := s.repo.Insert(ctx, trace); err != nil {
			// 留痕失败必须显式暴露：清空已发生，但审计链断裂。
			return deleted, fmt.Errorf("audit logs cleared (%d rows) but failed to persist clear-trace record: %w", deleted, err)
		}
	}
	return deleted, nil
}

func (s *AuditLogService) runWriter() {
	defer s.wg.Done()

	ticker := time.NewTicker(auditLogFlushInterval)
	defer ticker.Stop()

	batch := make([]*AuditLog, 0, auditLogBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		inserted, err := s.repo.BatchInsert(ctx, batch)
		cancel()
		if err != nil {
			failed := atomic.AddUint64(&s.writeFailed, uint64(len(batch)))
			_, _ = fmt.Fprintf(os.Stderr, "time=%s level=ERROR msg=\"audit log flush failed\" err=%v batch=%d write_failed_total=%d\n",
				time.Now().Format(time.RFC3339Nano), err, len(batch), failed)
		} else {
			atomic.AddUint64(&s.writtenCount, uint64(inserted))
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-s.ctx.Done():
			// 停机前排空队列。
			for {
				select {
				case item := <-s.queue:
					if item == nil {
						continue
					}
					batch = append(batch, item)
					if len(batch) >= auditLogBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case item := <-s.queue:
			if item == nil {
				continue
			}
			batch = append(batch, item)
			if len(batch) >= auditLogBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// runRetentionLoop 按保留期定期删除过期审计日志。
// 删除操作幂等，多实例并发执行无害，因此无需选主。
func (s *AuditLogService) runRetentionLoop() {
	defer s.wg.Done()

	startupTimer := time.NewTimer(auditRetentionStartupDelay)
	defer startupTimer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-startupTimer.C:
	}

	ticker := time.NewTicker(auditRetentionCheckInterval)
	defer ticker.Stop()

	s.runRetentionOnce()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runRetentionOnce()
		}
	}
}

func (s *AuditLogService) runRetentionOnce() {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()

	days := 0
	if s.settingService != nil {
		days = s.settingService.GetAuditLogRetentionDays(ctx)
	}
	if days <= 0 {
		return // 0 或负值表示永久保留，仅支持手动清空
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	for {
		deleted, err := s.repo.DeleteBefore(ctx, cutoff, auditRetentionBatchSize)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "time=%s level=WARN msg=\"audit log retention cleanup failed\" err=%v\n",
				time.Now().Format(time.RFC3339Nano), err)
			return
		}
		if deleted == 0 {
			return
		}
	}
}
