package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type competitiveWasteRepoStub struct {
	UsageLogRepository
	logs []CompetitiveWasteLog
}

func (s *competitiveWasteRepoStub) CreateCompetitiveWasteLogs(_ context.Context, logs []CompetitiveWasteLog) error {
	s.logs = append(s.logs, logs...)
	return nil
}

type contextCheckingCompetitiveWasteRepo struct {
	UsageLogRepository
	logs []CompetitiveWasteLog
}

func (r *contextCheckingCompetitiveWasteRepo) CreateCompetitiveWasteLogs(ctx context.Context, logs []CompetitiveWasteLog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.logs = append(r.logs, logs...)
	return nil
}

func TestGatewayCompetitiveWasteKeepsWinnerBillingSeparate(t *testing.T) {
	cfg := &config.Config{}
	repo := &competitiveWasteRepoStub{}
	svc := &GatewayService{
		usageLogRepo:   repo,
		billingService: NewBillingService(cfg, nil),
	}
	winnerUsage := ClaudeUsage{InputTokens: 100, OutputTokens: 20}
	loserUsage := ClaudeUsage{
		InputTokens:              80,
		OutputTokens:             2,
		CacheCreationInputTokens: 40,
		CacheCreation5mTokens:    40,
	}
	result := &ForwardResult{
		RequestID: "winner-request",
		Usage:     winnerUsage,
		Model:     "claude-sonnet-4",
		CompetitiveWasteAttempts: []CompetitiveWasteAttempt{
			{
				AttemptNo:             2,
				AccountID:             702,
				AccountRateMultiplier: 0.5,
				RequestID:             "loser-request",
				Model:                 "claude-sonnet-4",
				Usage:                 loserUsage,
				UsageReported:         true,
				Reason:                CompetitiveWasteReasonCanceledLoser,
			},
			{
				AttemptNo:             3,
				AccountID:             703,
				AccountRateMultiplier: 1,
				Model:                 "claude-sonnet-4",
				UsageReported:         false,
				Reason:                CompetitiveWasteReasonCanceledLoser,
			},
		},
	}
	apiKey := &APIKey{ID: 501}
	account := &Account{ID: 701}
	usageLog := &UsageLog{
		RequestID: "winner-request",
		UserID:    601,
		APIKeyID:  apiKey.ID,
		AccountID: account.ID,
		CreatedAt: time.Now(),
	}

	winnerCost := svc.calculateTokenCost(context.Background(), result, apiKey, result.Model, 1.1, time.Now(), &recordUsageOpts{})
	expectedWinnerCost, err := svc.billingService.CalculateCost("claude-sonnet-4", UsageTokens{
		InputTokens: winnerUsage.InputTokens, OutputTokens: winnerUsage.OutputTokens,
	}, 1.1)
	require.NoError(t, err)
	require.InDelta(t, expectedWinnerCost.ActualCost, winnerCost.ActualCost, 1e-12)
	require.Equal(t, winnerUsage, result.Usage)

	logs := svc.buildCompetitiveWasteLogs(context.Background(), result, usageLog, apiKey, account)
	require.Len(t, logs, 2)
	require.Equal(t, winnerUsage, result.Usage)
	require.Equal(t, loserUsage.InputTokens, logs[0].InputTokens)
	require.True(t, logs[0].UsageReported)
	require.NotNil(t, logs[0].CompetitiveWasteCost)
	loserCost, err := svc.billingService.CalculateCost("claude-sonnet-4", UsageTokens{
		InputTokens:           loserUsage.InputTokens,
		OutputTokens:          loserUsage.OutputTokens,
		CacheCreationTokens:   loserUsage.CacheCreationInputTokens,
		CacheCreation5mTokens: loserUsage.CacheCreation5mTokens,
	}, 1)
	require.NoError(t, err)
	require.InDelta(t, loserCost.TotalCost*0.5, *logs[0].CompetitiveWasteCost, 1e-12)
	require.False(t, logs[1].UsageReported)
	require.Nil(t, logs[1].CompetitiveWasteCost)

	svc.recordCompetitiveWaste(context.Background(), logs)
	require.Equal(t, logs, repo.logs)
}

func TestGatewayCompetitiveWasteUsesDetachedPersistenceContext(t *testing.T) {
	repo := &contextCheckingCompetitiveWasteRepo{}
	svc := &GatewayService{usageLogRepo: repo}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logs := []CompetitiveWasteLog{{LogicalRequestID: "winner-request", AttemptNo: 2}}

	svc.recordCompetitiveWaste(ctx, logs)

	require.Equal(t, logs, repo.logs)
}
