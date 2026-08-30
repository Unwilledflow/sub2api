package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

type CompetitiveWasteLog struct {
	LogicalRequestID      string
	UpstreamRequestID     string
	UserID                int64
	APIKeyID              int64
	GroupID               int64
	WinnerAccountID       int64
	AccountID             int64
	AttemptNo             int
	Model                 string
	UpstreamModel         string
	InputTokens           int
	OutputTokens          int
	CacheCreationTokens   int
	CacheReadTokens       int
	CacheCreation5mTokens int
	CacheCreation1hTokens int
	ImageOutputTokens     int
	UsageReported         bool
	CompetitiveWasteCost  *float64
	Reason                CompetitiveWasteReason
	DurationMs            int64
	CreatedAt             time.Time
}

type CompetitiveWasteLogRepository interface {
	CreateCompetitiveWasteLogs(ctx context.Context, logs []CompetitiveWasteLog) error
}

func (s *GatewayService) buildCompetitiveWasteLogs(
	ctx context.Context,
	result *ForwardResult,
	usageLog *UsageLog,
	apiKey *APIKey,
	account *Account,
) []CompetitiveWasteLog {
	if result == nil || usageLog == nil || apiKey == nil || account == nil || len(result.CompetitiveWasteAttempts) == 0 {
		return nil
	}

	logs := make([]CompetitiveWasteLog, 0, len(result.CompetitiveWasteAttempts))
	for _, attempt := range result.CompetitiveWasteAttempts {
		model := strings.TrimSpace(attempt.Model)
		if model == "" {
			model = strings.TrimSpace(result.Model)
		}
		upstreamModel := strings.TrimSpace(attempt.UpstreamModel)
		billingModel := forwardResultBillingModel(model, upstreamModel)
		if model == "" {
			model = billingModel
		}
		if model == "" {
			model = "unknown"
		}

		groupID := int64(0)
		if apiKey.GroupID != nil {
			groupID = *apiKey.GroupID
		}
		logs = append(logs, CompetitiveWasteLog{
			LogicalRequestID:      usageLog.RequestID,
			UpstreamRequestID:     strings.TrimSpace(attempt.RequestID),
			UserID:                usageLog.UserID,
			APIKeyID:              usageLog.APIKeyID,
			GroupID:               groupID,
			WinnerAccountID:       account.ID,
			AccountID:             attempt.AccountID,
			AttemptNo:             attempt.AttemptNo,
			Model:                 model,
			UpstreamModel:         upstreamModel,
			InputTokens:           attempt.Usage.InputTokens,
			OutputTokens:          attempt.Usage.OutputTokens,
			CacheCreationTokens:   attempt.Usage.CacheCreationInputTokens,
			CacheReadTokens:       attempt.Usage.CacheReadInputTokens,
			CacheCreation5mTokens: attempt.Usage.CacheCreation5mTokens,
			CacheCreation1hTokens: attempt.Usage.CacheCreation1hTokens,
			ImageOutputTokens:     attempt.Usage.ImageOutputTokens,
			UsageReported:         attempt.UsageReported,
			CompetitiveWasteCost:  s.calculateCompetitiveWasteCost(ctx, apiKey.GroupID, attempt, billingModel),
			Reason:                attempt.Reason,
			DurationMs:            attempt.Duration.Milliseconds(),
			CreatedAt:             usageLog.CreatedAt,
		})
	}
	return logs
}

func (s *GatewayService) calculateCompetitiveWasteCost(
	ctx context.Context,
	groupID *int64,
	attempt CompetitiveWasteAttempt,
	billingModel string,
) *float64 {
	if !attempt.UsageReported || s == nil || s.billingService == nil || strings.TrimSpace(billingModel) == "" {
		return nil
	}

	tokens := UsageTokens{
		InputTokens:           attempt.Usage.InputTokens,
		OutputTokens:          attempt.Usage.OutputTokens,
		CacheCreationTokens:   attempt.Usage.CacheCreationInputTokens,
		CacheReadTokens:       attempt.Usage.CacheReadInputTokens,
		CacheCreation5mTokens: attempt.Usage.CacheCreation5mTokens,
		CacheCreation1hTokens: attempt.Usage.CacheCreation1hTokens,
		ImageOutputTokens:     attempt.Usage.ImageOutputTokens,
	}
	listCost, err := s.billingService.CalculateCost(billingModel, tokens, 1)
	if err != nil || listCost == nil {
		return nil
	}

	baseCost := listCost.TotalCost
	if groupID != nil {
		if resolved := resolveAccountStatsCost(
			ctx,
			s.channelService,
			s.billingService,
			attempt.AccountID,
			*groupID,
			billingModel,
			tokens,
			1,
			listCost.TotalCost,
			"",
		); resolved != nil {
			baseCost = *resolved
		}
	}

	rate := attempt.AccountRateMultiplier
	if rate < 0 {
		rate = 1
	}
	cost := baseCost * rate
	return &cost
}

func (s *GatewayService) recordCompetitiveWaste(ctx context.Context, logs []CompetitiveWasteLog) {
	if len(logs) == 0 || s == nil {
		return
	}
	repo, ok := s.usageLogRepo.(CompetitiveWasteLogRepository)
	if !ok || repo == nil {
		logger.L().Warn("gateway.competitive_waste_repository_unavailable",
			zap.Int("attempt_count", len(logs)),
		)
		return
	}

	persistCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	if err := repo.CreateCompetitiveWasteLogs(persistCtx, logs); err != nil {
		logger.L().Warn("gateway.competitive_waste_persist_failed",
			zap.String("request_id", logs[0].LogicalRequestID),
			zap.Int("attempt_count", len(logs)),
			zap.Error(err),
		)
		return
	}

	reported := 0
	priced := 0
	totalCost := 0.0
	for _, log := range logs {
		if log.UsageReported {
			reported++
		}
		if log.CompetitiveWasteCost != nil {
			priced++
			totalCost += *log.CompetitiveWasteCost
		}
	}
	logger.L().Info("gateway.competitive_waste_recorded",
		zap.String("request_id", logs[0].LogicalRequestID),
		zap.Int("attempt_count", len(logs)),
		zap.Int("reported_usage_attempts", reported),
		zap.Int("unreported_usage_attempts", len(logs)-reported),
		zap.Int("priced_attempts", priced),
		zap.Float64("competitive_waste_cost", totalCost),
	)
}
