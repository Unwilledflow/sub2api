package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type openAI403CounterResetStub struct {
	resetCalls []int64
}

type openAIPool5xxSuccessResetStub struct {
	resetCalls      []int64
	failures        int
	resetContextErr error
}

func (s *openAIPool5xxSuccessResetStub) ObserveOpenAIPool5xxFailure(context.Context, int64, int, int) (int64, bool, error) {
	s.failures++
	return int64(s.failures), true, nil
}

func (s *openAIPool5xxSuccessResetStub) ResetOpenAIPool5xxCount(ctx context.Context, accountID int64) error {
	s.resetCalls = append(s.resetCalls, accountID)
	s.failures = 0
	s.resetContextErr = ctx.Err()
	return nil
}

func (s *openAIPool5xxSuccessResetStub) ObserveFailure(accountID int64) error {
	_, _, err := s.ObserveOpenAIPool5xxFailure(context.Background(), accountID, 60, 1)
	return err
}

func (*openAIPool5xxSuccessResetStub) ClearOpenAIPool5xxState(context.Context, int64) error {
	return nil
}

func (s *openAI403CounterResetStub) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return 0, nil
}

func (s *openAI403CounterResetStub) ResetOpenAI403Count(_ context.Context, accountID int64) error {
	s.resetCalls = append(s.resetCalls, accountID)
	return nil
}

func TestOpenAIGatewayServiceRecordUsage_ResetsOnlyOpenAI403CounterForZeroUsage(t *testing.T) {
	counter := &openAI403CounterResetStub{}
	pool5xxCounter := &openAIPool5xxSuccessResetStub{}
	rateLimitSvc := NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitSvc.SetOpenAI403CounterCache(counter)
	rateLimitSvc.SetOpenAIPool5xxCounterCache(pool5xxCounter)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
	svc.rateLimitService = rateLimitSvc

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID: "resp_zero_usage_reset_403",
			Model:     "gpt-5.1",
		},
		APIKey:  &APIKey{ID: 1001, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2001},
		Account: &Account{ID: 777, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.Equal(t, []int64{777}, counter.resetCalls)
	require.Empty(t, pool5xxCounter.resetCalls)
	require.Equal(t, 1, usageRepo.calls)
}

func TestOpenAIGatewayServiceRecordUsage_DoesNotReset5xxStreakForFailedWebSocketTurn(t *testing.T) {
	pool5xxCounter := &openAIPool5xxSuccessResetStub{}
	rateLimitSvc := NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitSvc.SetOpenAIPool5xxCounterCache(pool5xxCounter)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.rateLimitService = rateLimitSvc

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:             "resp_failed_ws_keeps_5xx_streak",
			Model:                 "gpt-5.6-sol",
			OpenAIWSMode:          true,
			UpstreamTerminalEvent: "response.failed",
		},
		APIKey:  &APIKey{ID: 1002, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2002},
		Account: &Account{ID: 778, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.Empty(t, pool5xxCounter.resetCalls)
	require.Equal(t, 1, usageRepo.calls)
}

func TestOpenAIGatewayServiceRecordUsage_DelayedBillingDoesNotEraseNewer5xxFailure(t *testing.T) {
	pool5xxCounter := &openAIPool5xxSuccessResetStub{}
	rateLimitSvc := NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitSvc.SetOpenAIPool5xxCounterCache(pool5xxCounter)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.rateLimitService = rateLimitSvc
	account := &Account{ID: 779, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	svc.RecordOpenAIUpstreamSuccess(context.Background(), account, &OpenAIForwardResult{}, false)
	require.NoError(t, pool5xxCounter.ObserveFailure(account.ID))
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result:  &OpenAIForwardResult{RequestID: "resp_delayed_success", Model: "gpt-5.6-sol"},
		APIKey:  &APIKey{ID: 1003, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2003},
		Account: account,
	})

	require.NoError(t, err)
	require.Equal(t, 1, pool5xxCounter.failures)
	require.Equal(t, []int64{779}, pool5xxCounter.resetCalls)
}

func TestRateLimitServiceRecordOpenAIUpstreamSuccess_OnlyResetsOpenAIAPIKeys(t *testing.T) {
	counter := &openAIPool5xxSuccessResetStub{}
	svc := NewRateLimitService(nil, nil, nil, nil, nil)
	svc.SetOpenAIPool5xxCounterCache(counter)

	svc.RecordOpenAIUpstreamSuccess(context.Background(), &Account{ID: 625, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	svc.RecordOpenAIUpstreamSuccess(context.Background(), &Account{ID: 626, Platform: PlatformOpenAI, Type: AccountTypeOAuth})
	svc.RecordOpenAIUpstreamSuccess(context.Background(), &Account{ID: 627, Platform: PlatformGrok, Type: AccountTypeAPIKey})

	require.Equal(t, []int64{625}, counter.resetCalls)
}

func TestOpenAIGatewayServiceRecordOpenAIUpstreamSuccess_DetachesCancelledRequest(t *testing.T) {
	counter := &openAIPool5xxSuccessResetStub{}
	rateLimitSvc := NewRateLimitService(nil, nil, nil, nil, nil)
	rateLimitSvc.SetOpenAIPool5xxCounterCache(counter)
	svc := &OpenAIGatewayService{rateLimitService: rateLimitSvc}
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()

	svc.RecordOpenAIUpstreamSuccess(
		requestCtx,
		&Account{ID: 628, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		&OpenAIForwardResult{},
		false,
	)

	require.Equal(t, []int64{628}, counter.resetCalls)
	require.NoError(t, counter.resetContextErr)
}
