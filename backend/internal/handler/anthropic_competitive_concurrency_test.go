package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type competitiveAnthropicStickyCache struct {
	bindings    map[string]int64
	setCalls    int
	deleteCalls int
}

func (c *competitiveAnthropicStickyCache) GetSessionAccountID(_ context.Context, _ int64, sessionHash string) (int64, error) {
	accountID, ok := c.bindings[sessionHash]
	if !ok {
		return 0, service.ErrStickySessionNotFound
	}
	return accountID, nil
}

func (c *competitiveAnthropicStickyCache) SetSessionAccountID(_ context.Context, _ int64, sessionHash string, accountID int64, _ time.Duration) error {
	if c.bindings == nil {
		c.bindings = make(map[string]int64)
	}
	c.setCalls++
	c.bindings[sessionHash] = accountID
	return nil
}

func (c *competitiveAnthropicStickyCache) RefreshSessionTTL(_ context.Context, _ int64, _ string, _ time.Duration) error {
	return nil
}

func (c *competitiveAnthropicStickyCache) DeleteSessionAccountID(_ context.Context, _ int64, sessionHash string) error {
	c.deleteCalls++
	delete(c.bindings, sessionHash)
	return nil
}

func newCompetitiveAnthropicGatewayServiceForTest(cache service.GatewayCache) *service.GatewayService {
	return service.NewGatewayService(
		nil, nil, nil, nil, nil, nil, nil, cache,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
}

func TestRunCompetitiveAnthropicForwardSlowWinnerClearsStickySession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const sessionHash = "slow-winner-session"
	groupID := int64(97)
	winnerAccount := &service.Account{ID: 2286}
	cache := &competitiveAnthropicStickyCache{bindings: map[string]int64{sessionHash: 2095}}
	forward := func(_ context.Context, attemptGin *gin.Context, _ *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"slow winner\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	winner, _, _, err := (&GatewayHandler{gatewayService: newCompetitiveAnthropicGatewayServiceForTest(cache)}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(-6*time.Second),
		&groupID,
		sessionHash,
		&service.AccountSelectionResult{Account: winnerAccount, Acquired: true},
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		nil,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, winnerAccount.ID, winner.ID)
	require.NotContains(t, cache.bindings, sessionHash)
	require.Zero(t, cache.setCalls)
	require.Equal(t, 1, cache.deleteCalls)
	require.False(t, competitiveAnthropicStickyBindAllowed(c))
}

func TestRunCompetitiveAnthropicForwardWinnerDoesNotOverwriteDifferentStickyBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const sessionHash = "bound-other-session"
	groupID := int64(97)
	boundAccountID := int64(2095)
	winnerAccount := &service.Account{ID: 2288}
	cache := &competitiveAnthropicStickyCache{bindings: map[string]int64{sessionHash: boundAccountID}}
	forward := func(_ context.Context, attemptGin *gin.Context, _ *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"fast winner\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	winner, _, _, err := (&GatewayHandler{gatewayService: newCompetitiveAnthropicGatewayServiceForTest(cache)}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(competitiveConcurrencyFirstOutput),
		&groupID,
		sessionHash,
		&service.AccountSelectionResult{Account: winnerAccount, Acquired: true},
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		nil,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, winnerAccount.ID, winner.ID)
	require.Equal(t, boundAccountID, cache.bindings[sessionHash])
	require.Zero(t, cache.setCalls)
	require.Zero(t, cache.deleteCalls)
}

func TestRunCompetitiveAnthropicForwardFastWinnerKeepsStickySession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const sessionHash = "fast-winner-session"
	groupID := int64(97)
	winnerAccount := &service.Account{ID: 2288}
	cache := &competitiveAnthropicStickyCache{bindings: make(map[string]int64)}
	forward := func(_ context.Context, attemptGin *gin.Context, _ *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"fast winner\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	winner, _, _, err := (&GatewayHandler{gatewayService: newCompetitiveAnthropicGatewayServiceForTest(cache)}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(competitiveConcurrencyFirstOutput),
		&groupID,
		sessionHash,
		&service.AccountSelectionResult{Account: winnerAccount, Acquired: true},
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		nil,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, winnerAccount.ID, winner.ID)
	require.Equal(t, winnerAccount.ID, cache.bindings[sessionHash])
	require.Equal(t, 1, cache.setCalls)
	require.Zero(t, cache.deleteCalls)
	require.True(t, competitiveAnthropicStickyBindAllowed(c))
}

func TestRunCompetitiveAnthropicForwardSkipsSaturatedAndUsesFirstSemanticDelta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	initialAccount := &service.Account{ID: 1, Name: "slow"}
	saturatedAccount := &service.Account{ID: 2, Name: "full"}
	fastAccount := &service.Account{ID: 3, Name: "fast"}
	initialSelection := &service.AccountSelectionResult{
		Account:  initialAccount,
		Acquired: true,
	}

	var initialReleased atomic.Int32
	initialRelease := func() { initialReleased.Add(1) }
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		switch selectCalls {
		case 1:
			return &service.AccountSelectionResult{Account: saturatedAccount, Acquired: false}, nil
		case 2:
			return &service.AccountSelectionResult{Account: fastAccount, Acquired: true}, nil
		default:
			return nil, service.ErrNoAvailableAccounts
		}
	}

	var slowCanceled atomic.Bool
	forward := func(ctx context.Context, attemptGin *gin.Context, account *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		if account.ID == initialAccount.ID {
			_, err := attemptGin.Writer.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
			require.NoError(t, err)
			<-ctx.Done()
			slowCanceled.Store(true)
			return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 80}}, nil, ctx.Err()
		}

		time.Sleep(20 * time.Millisecond)
		_, err := attemptGin.Writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"fast\"}}\n\n"))
		require.NoError(t, err)
		return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 100, OutputTokens: 10}}, nil, nil
	}

	h := &GatewayHandler{gatewayService: &service.GatewayService{}}
	winner, result, _, err := h.runCompetitiveAnthropicForward(
		c,
		time.Now().Add(competitiveConcurrencyFirstOutput),
		nil,
		"session",
		initialSelection,
		initialRelease,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, fastAccount.ID, winner.ID)
	require.Equal(t, 100, result.Usage.InputTokens)
	require.Equal(t, 10, result.Usage.OutputTokens)
	require.Len(t, result.CompetitiveWasteAttempts, 1)
	require.Equal(t, initialAccount.ID, result.CompetitiveWasteAttempts[0].AccountID)
	require.True(t, result.CompetitiveWasteAttempts[0].UsageReported)
	require.Equal(t, 80, result.CompetitiveWasteAttempts[0].Usage.InputTokens)
	require.Contains(t, recorder.Body.String(), "text_delta")
	require.NotContains(t, recorder.Body.String(), "message_start")
	require.True(t, slowCanceled.Load())
	require.Equal(t, int32(1), initialReleased.Load())
	require.Equal(t, 3, selectCalls)
}

func TestCollectCompetitiveAnthropicWasteDoesNotChangeCustomerUsage(t *testing.T) {
	winner := &service.ForwardResult{Usage: service.ClaudeUsage{
		InputTokens:              100,
		OutputTokens:             20,
		CacheCreationInputTokens: 300,
		CacheReadInputTokens:     400,
		CacheCreation5mTokens:    250,
		CacheCreation1hTokens:    50,
	}}
	candidates := []competitiveAnthropicCandidate{
		{account: &service.Account{ID: 1}, attemptNo: 1},
		{account: &service.Account{ID: 2}, attemptNo: 2},
		{account: &service.Account{ID: 3}, attemptNo: 3},
	}
	outcomes := map[int]competitiveAnthropicOutcome{
		0: {id: 0, result: winner},
		1: {id: 1, result: &service.ForwardResult{Usage: service.ClaudeUsage{
			InputTokens:              90,
			OutputTokens:             2,
			CacheCreationInputTokens: 200,
			CacheReadInputTokens:     350,
			CacheCreation5mTokens:    180,
			CacheCreation1hTokens:    20,
		}}},
	}

	collectCompetitiveAnthropicWaste(winner, 0, "claude-opus-4-6", candidates, outcomes, service.CompetitiveWasteReasonCanceledLoser)

	require.Equal(t, 100, winner.Usage.InputTokens)
	require.Equal(t, 20, winner.Usage.OutputTokens)
	require.Equal(t, 300, winner.Usage.CacheCreationInputTokens)
	require.Equal(t, 400, winner.Usage.CacheReadInputTokens)
	require.Equal(t, 250, winner.Usage.CacheCreation5mTokens)
	require.Equal(t, 50, winner.Usage.CacheCreation1hTokens)
	require.Len(t, winner.CompetitiveWasteAttempts, 2)
	require.Equal(t, int64(2), winner.CompetitiveWasteAttempts[0].AccountID)
	require.Equal(t, 2, winner.CompetitiveWasteAttempts[0].AttemptNo)
	require.True(t, winner.CompetitiveWasteAttempts[0].UsageReported)
	require.Equal(t, 90, winner.CompetitiveWasteAttempts[0].Usage.InputTokens)
	require.Equal(t, int64(3), winner.CompetitiveWasteAttempts[1].AccountID)
	require.Equal(t, 3, winner.CompetitiveWasteAttempts[1].AttemptNo)
	require.False(t, winner.CompetitiveWasteAttempts[1].UsageReported)
	require.False(t, claudeUsageHasTokens(winner.CompetitiveWasteAttempts[1].Usage))
}

func TestLogCompetitiveAnthropicSettlementEvidenceUsesWinnerOnlyPolicy(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	reqLog := zap.New(core)
	candidates := []competitiveAnthropicCandidate{
		{account: &service.Account{ID: 701}},
		{account: &service.Account{ID: 702}},
		{account: &service.Account{ID: 703}},
	}
	outcomes := map[int]competitiveAnthropicOutcome{
		0: {result: &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 100}}},
		1: {result: &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 80}}},
		2: {err: errors.New("canceled")},
	}

	logCompetitiveAnthropicSettlementEvidence(reqLog, 0, time.Now(), candidates, outcomes, outcomes[0].result)

	require.Equal(t, 1, observed.Len())
	fields := observed.All()[0].ContextMap()
	require.Equal(t, "winner_only", fields["customer_usage_policy"])
	require.Equal(t, true, fields["winner_available"])
	require.Equal(t, int64(2), fields["internal_waste_attempt_count"])
	require.Equal(t, int64(1), fields["reported_waste_usage_attempts"])
	require.Equal(t, int64(1), fields["unreported_waste_usage_attempts"])
	require.NotContains(t, fields, "usage_aggregated_into_winner")
}

func TestCompetitivePayloadRecognizesAnthropicSemanticEvents(t *testing.T) {
	header := http.Header{"Content-Type": []string{"text/event-stream"}}
	require.False(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"message_start\"}\n\n")))
	require.False(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"search\"}}\n\n")))
}

func TestCompetitiveAnthropicInheritsRootGroupSwitchAcrossAdaptiveLeaf(t *testing.T) {
	rootAPIKey := &service.APIKey{Group: &service.Group{CompetitiveConcurrency: true}}
	leafAPIKey := &service.APIKey{Group: &service.Group{CompetitiveConcurrency: false}}

	// ours 定制：竞争并发（hedging）整体停用（groupCompetitiveConcurrencyEnabled
	// 恒 false，避免向用户重复计落选候选的 input），root/leaf 任何组合都不启用。
	require.False(t, competitiveAnthropicEnabled(rootAPIKey, leafAPIKey))
	require.False(t, competitiveAnthropicEnabled(leafAPIKey, rootAPIKey))
	require.False(t, competitiveAnthropicEnabled(leafAPIKey, leafAPIKey))
}

func TestCompetitiveCoordinatorCancelAllRejectsLateSemanticOutput(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	coordinator := newCompetitiveResponseCoordinator(c.Writer)
	writer := newCompetitiveResponseWriter(0, coordinator, http.Header{
		"Content-Type": []string{"text/event-stream"},
	})

	coordinator.cancelAll()
	_, err := writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"late\"}}\n\n"))

	require.NoError(t, err)
	require.Empty(t, recorder.Body.String())
}

func TestRunCompetitiveAnthropicForwardStartsHedgeOnlyAfterFirstOutputDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	accounts := []*service.Account{{ID: 11, Name: "slow"}, {ID: 12, Name: "hedge"}}
	initialSelection := &service.AccountSelectionResult{Account: accounts[0], Acquired: true}
	var released atomic.Int32
	var selectedAt atomic.Int64
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		if selectCalls == 1 {
			selectedAt.Store(time.Now().UnixNano())
			return &service.AccountSelectionResult{
				Account:     accounts[1],
				Acquired:    true,
				ReleaseFunc: func() { released.Add(1) },
			}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}

	var canceled atomic.Int32
	forward := func(ctx context.Context, attemptGin *gin.Context, account *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		if account.ID == accounts[0].ID {
			_, err := attemptGin.Writer.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
			require.NoError(t, err)
			<-ctx.Done()
			canceled.Add(1)
			return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 80}}, nil, ctx.Err()
		}
		_, err := attemptGin.Writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hedged\"}}\n\n"))
		return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 100, OutputTokens: 10}}, nil, err
	}

	const hedgeDelay = 75 * time.Millisecond
	startedAt := time.Now()
	winner, result, parsed, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		startedAt.Add(hedgeDelay),
		nil,
		"session",
		initialSelection,
		func() { released.Add(1) },
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)
	elapsed := time.Since(startedAt)

	require.NoError(t, err)
	require.Equal(t, accounts[1].ID, winner.ID)
	require.NotNil(t, result)
	require.Nil(t, parsed)
	require.Contains(t, recorder.Body.String(), "hedged")
	require.Equal(t, 2, selectCalls)
	require.GreaterOrEqual(t, time.Duration(selectedAt.Load()-startedAt.UnixNano()), hedgeDelay)
	require.GreaterOrEqual(t, elapsed, hedgeDelay)
	require.Less(t, elapsed, time.Second)
	require.Eventually(t, func() bool {
		return canceled.Load() == 1 && released.Load() == int32(len(accounts))
	}, time.Second, 10*time.Millisecond)
}

func TestRunCompetitiveAnthropicForwardExpiredDeadlineStartsCompetitionInsteadOfTimingOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	initialAccount := &service.Account{ID: 15}
	hedgeAccount := &service.Account{ID: 16}
	initialSelection := &service.AccountSelectionResult{Account: initialAccount, Acquired: true}
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		if selectCalls == 1 {
			return &service.AccountSelectionResult{Account: hedgeAccount, Acquired: true}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(ctx context.Context, attemptGin *gin.Context, account *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		if account.ID == initialAccount.ID {
			_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"message_start\"}\n\n"))
			require.NoError(t, err)
			<-ctx.Done()
			return &service.ForwardResult{}, nil, ctx.Err()
		}
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"late-selection-recovered\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	winner, result, _, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(-time.Millisecond),
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, hedgeAccount.ID, winner.ID)
	require.NotNil(t, result)
	require.Equal(t, 2, selectCalls)
	require.Contains(t, recorder.Body.String(), "late-selection-recovered")
}

func TestRunCompetitiveAnthropicForwardFastInitialSkipsHedgeSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	initialAccount := &service.Account{ID: 21}
	initialSelection := &service.AccountSelectionResult{Account: initialAccount, Acquired: true}
	var released atomic.Int32
	var selectCalls atomic.Int32
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls.Add(1)
		return nil, service.ErrNoAvailableAccounts
	}
	var forwarded atomic.Int32
	forward := func(_ context.Context, attemptGin *gin.Context, _ *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		forwarded.Add(1)
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"late\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	startedAt := time.Now()
	winner, result, parsed, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(time.Second),
		nil,
		"session",
		initialSelection,
		func() { released.Add(1) },
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)
	elapsed := time.Since(startedAt)

	require.NoError(t, err)
	require.Equal(t, initialAccount.ID, winner.ID)
	require.NotNil(t, result)
	require.Nil(t, parsed)
	require.Equal(t, int32(1), forwarded.Load())
	require.Zero(t, selectCalls.Load())
	require.Equal(t, int32(1), released.Load())
	require.Contains(t, recorder.Body.String(), "late")
	require.Less(t, elapsed, time.Second)
}

func TestRunCompetitiveAnthropicForwardInitialFailureStartsHedgeImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	initialAccount := &service.Account{ID: 31}
	hedgeAccount := &service.Account{ID: 32}
	initialSelection := &service.AccountSelectionResult{Account: initialAccount, Acquired: true}
	var initialStarted atomic.Bool
	var selectedBeforeInitial atomic.Bool
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		if !initialStarted.Load() {
			selectedBeforeInitial.Store(true)
		}
		selectCalls++
		if selectCalls == 1 {
			return &service.AccountSelectionResult{Account: hedgeAccount, Acquired: true}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(_ context.Context, attemptGin *gin.Context, account *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		if account.ID == initialAccount.ID {
			initialStarted.Store(true)
			return nil, nil, errors.New("primary failed")
		}
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"recovered\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	startedAt := time.Now()
	winner, _, _, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		startedAt.Add(time.Second),
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, hedgeAccount.ID, winner.ID)
	require.False(t, selectedBeforeInitial.Load())
	require.Less(t, time.Since(startedAt), time.Second)
	require.Contains(t, recorder.Body.String(), "recovered")
}

func TestRunCompetitiveAnthropicForwardWinnerCancelsBlockingHedgeSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestCtx, cancelRequest := context.WithTimeout(context.Background(), time.Second)
	defer cancelRequest()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)

	initialAccount := &service.Account{ID: 71}
	initialSelection := &service.AccountSelectionResult{Account: initialAccount, Acquired: true}
	selectionStarted := make(chan struct{})
	selectNext := func(ctx context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		close(selectionStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	forward := func(_ context.Context, attemptGin *gin.Context, _ *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		<-selectionStarted
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"winner\"}}\n\n"))
		return &service.ForwardResult{}, nil, err
	}

	startedAt := time.Now()
	winner, _, _, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		startedAt.Add(20*time.Millisecond),
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, initialAccount.ID, winner.ID)
	require.Less(t, time.Since(startedAt), time.Second)
	require.Contains(t, recorder.Body.String(), "winner")
}

func TestRunCompetitiveAnthropicForwardPrefersDistinctRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := func(id int64, baseURL string) *service.Account {
		return &service.Account{
			ID:          id,
			Platform:    service.PlatformAnthropic,
			Type:        service.AccountTypeAPIKey,
			Credentials: map[string]any{"base_url": baseURL},
		}
	}
	initial := account(81, "https://route-a.example")
	duplicate := account(82, "https://route-a.example/")
	fastDistinct := account(83, "https://route-b.example")
	slowDistinct := account(84, "https://route-c.example")
	initialSelection := &service.AccountSelectionResult{Account: initial, Acquired: true}

	selectCalls := 0
	var duplicateReleased atomic.Int32
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		switch selectCalls {
		case 1:
			return &service.AccountSelectionResult{
				Account:     duplicate,
				Acquired:    true,
				ReleaseFunc: func() { duplicateReleased.Add(1) },
			}, nil
		case 2:
			return &service.AccountSelectionResult{Account: fastDistinct, Acquired: true}, nil
		case 3:
			return &service.AccountSelectionResult{Account: slowDistinct, Acquired: true}, nil
		default:
			return nil, service.ErrNoAvailableAccounts
		}
	}

	forwarded := make(map[int64]bool)
	var forwardedMu sync.Mutex
	forward := func(ctx context.Context, attemptGin *gin.Context, selected *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		forwardedMu.Lock()
		forwarded[selected.ID] = true
		forwardedMu.Unlock()
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		if selected.ID == fastDistinct.ID {
			_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"distinct\"}}\n\n"))
			return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 100, OutputTokens: 10}}, nil, err
		}
		<-ctx.Done()
		return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 80}}, nil, ctx.Err()
	}

	winner, _, _, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(20*time.Millisecond),
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, fastDistinct.ID, winner.ID)
	require.Equal(t, 2, selectCalls)
	require.Eventually(t, func() bool {
		return duplicateReleased.Load() == 1
	}, time.Second, 10*time.Millisecond)
	forwardedMu.Lock()
	defer forwardedMu.Unlock()
	require.True(t, forwarded[initial.ID])
	require.True(t, forwarded[fastDistinct.ID])
	require.False(t, forwarded[slowDistinct.ID])
	require.False(t, forwarded[duplicate.ID])
}

func TestRunCompetitiveAnthropicForwardFallsBackToDuplicateRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	initial := &service.Account{
		ID:          91,
		Platform:    service.PlatformAnthropic,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://only-route.example"},
	}
	duplicate := &service.Account{
		ID:          92,
		Platform:    service.PlatformAnthropic,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://only-route.example/"},
	}
	initialSelection := &service.AccountSelectionResult{Account: initial, Acquired: true}
	var duplicateReleased atomic.Int32
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		if selectCalls == 1 {
			return &service.AccountSelectionResult{
				Account:     duplicate,
				Acquired:    true,
				ReleaseFunc: func() { duplicateReleased.Add(1) },
			}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(ctx context.Context, attemptGin *gin.Context, selected *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		if selected.ID == duplicate.ID {
			_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"fallback\"}}\n\n"))
			return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 100, OutputTokens: 10}}, nil, err
		}
		<-ctx.Done()
		return &service.ForwardResult{Usage: service.ClaudeUsage{InputTokens: 80}}, nil, ctx.Err()
	}

	winner, _, _, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(20*time.Millisecond),
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, duplicate.ID, winner.ID)
	require.Equal(t, 2, selectCalls)
	require.Eventually(t, func() bool {
		return duplicateReleased.Load() == 1
	}, time.Second, 10*time.Millisecond)
	require.Contains(t, recorder.Body.String(), "fallback")
}

func TestCompetitiveAnthropicRouteKeyNormalizesEndpointAndDistinguishesProxy(t *testing.T) {
	proxy1 := int64(1)
	proxy2 := int64(2)
	account := func(id, proxyID int64, baseURL string) *service.Account {
		return &service.Account{
			ID:          id,
			Platform:    service.PlatformAnthropic,
			Type:        service.AccountTypeAPIKey,
			Credentials: map[string]any{"base_url": baseURL},
			ProxyID:     &proxyID,
		}
	}

	first := account(101, proxy1, "HTTPS://Relay.Example/v1/")
	second := account(102, proxy1, "https://relay.example/v1")
	third := account(103, proxy2, "https://relay.example/v1")

	require.Equal(t, competitiveAnthropicRouteKey(first), competitiveAnthropicRouteKey(second))
	require.NotEqual(t, competitiveAnthropicRouteKey(first), competitiveAnthropicRouteKey(third))
}

func TestCompetitiveAnthropicRouteKeyUsesEnabledCustomRelayForOAuth(t *testing.T) {
	account := func(id int64, accountType, relay string, enabled bool) *service.Account {
		return &service.Account{
			ID:       id,
			Platform: service.PlatformAnthropic,
			Type:     accountType,
			Extra: map[string]any{
				"custom_base_url_enabled": enabled,
				"custom_base_url":         relay,
			},
		}
	}

	first := account(111, service.AccountTypeOAuth, "HTTPS://Relay-A.Example/v1/", true)
	second := account(112, service.AccountTypeSetupToken, "https://relay-a.example/v1", true)
	third := account(113, service.AccountTypeOAuth, "https://relay-b.example/v1", true)
	disabled := account(114, service.AccountTypeOAuth, "https://relay-b.example/v1", false)

	require.Equal(t, competitiveAnthropicRouteKey(first), competitiveAnthropicRouteKey(second))
	require.NotEqual(t, competitiveAnthropicRouteKey(first), competitiveAnthropicRouteKey(third))
	require.NotEqual(t, competitiveAnthropicRouteKey(third), competitiveAnthropicRouteKey(disabled))
}

func TestCompetitiveAnthropicFailedBatchUsageCarriesToLaterSuccessExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	accounts := []*service.Account{{ID: 121}, {ID: 122}}
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		if selectCalls == 1 {
			return &service.AccountSelectionResult{Account: accounts[selectCalls], Acquired: true}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(_ context.Context, _ *gin.Context, account *service.Account) (*service.ForwardResult, *service.ParsedRequest, error) {
		switch account.ID {
		case accounts[0].ID:
			return &service.ForwardResult{Usage: service.ClaudeUsage{
				InputTokens: 11, OutputTokens: 1, CacheCreationInputTokens: 5,
				CacheReadInputTokens: 7, CacheCreation5mTokens: 3, CacheCreation1hTokens: 2,
				ImageOutputTokens: 4,
			}}, nil, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
		case accounts[1].ID:
			return &service.ForwardResult{Usage: service.ClaudeUsage{
				InputTokens: 22, OutputTokens: 2, CacheCreationInputTokens: 10,
				CacheReadInputTokens: 14, CacheCreation5mTokens: 6, CacheCreation1hTokens: 4,
				ImageOutputTokens: 8,
			}}, nil, &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
		}
		return nil, nil, &service.UpstreamFailoverError{StatusCode: http.StatusGatewayTimeout}
	}

	_, _, _, err := (&GatewayHandler{gatewayService: &service.GatewayService{}}).runCompetitiveAnthropicForward(
		c,
		time.Now().Add(time.Hour),
		nil,
		"session",
		&service.AccountSelectionResult{Account: accounts[0], Acquired: true},
		nil,
		map[int64]struct{}{},
		"claude-opus-4-6",
		selectNext,
		forward,
		zap.NewNop(),
	)
	require.Error(t, err)

	success := &service.ForwardResult{Usage: service.ClaudeUsage{
		InputTokens: 100, OutputTokens: 10, CacheCreationInputTokens: 20,
		CacheReadInputTokens: 30, CacheCreation5mTokens: 10, CacheCreation1hTokens: 10,
		ImageOutputTokens: 1,
	}}
	mergePendingCompetitiveAnthropicWaste(c, success)
	require.Equal(t, 100, success.Usage.InputTokens)
	require.Equal(t, 10, success.Usage.OutputTokens)
	require.Equal(t, 20, success.Usage.CacheCreationInputTokens)
	require.Equal(t, 30, success.Usage.CacheReadInputTokens)
	require.Equal(t, 10, success.Usage.CacheCreation5mTokens)
	require.Equal(t, 10, success.Usage.CacheCreation1hTokens)
	require.Equal(t, 1, success.Usage.ImageOutputTokens)
	require.Len(t, success.CompetitiveWasteAttempts, 2)
	require.Equal(t, accounts[0].ID, success.CompetitiveWasteAttempts[0].AccountID)
	require.Equal(t, 11, success.CompetitiveWasteAttempts[0].Usage.InputTokens)
	require.Equal(t, service.CompetitiveWasteReasonFailedBatch, success.CompetitiveWasteAttempts[0].Reason)
	require.Equal(t, accounts[1].ID, success.CompetitiveWasteAttempts[1].AccountID)
	require.Equal(t, 22, success.CompetitiveWasteAttempts[1].Usage.InputTokens)
	require.Equal(t, service.CompetitiveWasteReasonFailedBatch, success.CompetitiveWasteAttempts[1].Reason)

	mergePendingCompetitiveAnthropicWaste(c, success)
	require.Equal(t, 100, success.Usage.InputTokens)
	require.Equal(t, 10, success.Usage.OutputTokens)
	require.Equal(t, 20, success.Usage.CacheCreationInputTokens)
	require.Equal(t, 30, success.Usage.CacheReadInputTokens)
	require.Equal(t, 10, success.Usage.CacheCreation5mTokens)
	require.Equal(t, 10, success.Usage.CacheCreation1hTokens)
	require.Equal(t, 1, success.Usage.ImageOutputTokens)
	require.Len(t, success.CompetitiveWasteAttempts, 2)
}
