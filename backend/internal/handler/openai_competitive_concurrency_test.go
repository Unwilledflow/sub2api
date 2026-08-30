package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCompetitivePayloadIsMeaningful(t *testing.T) {
	header := http.Header{"Content-Type": []string{"text/event-stream"}}
	require.False(t, competitivePayloadIsMeaningful(header, []byte(": keepalive\n\n")))
	require.False(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"response.failed\"}\n\n")))
	require.False(t, competitivePayloadIsMeaningful(header, []byte("data: {\"error\":{\"message\":\"bad\"}}\n\n")))
	require.False(t, competitivePayloadIsMeaningful(header, []byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n")))
	require.False(t, competitivePayloadIsMeaningful(header, []byte("data: {invalid json}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"step\"}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(header, []byte("data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\"}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(header, []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")))
	require.True(t, competitivePayloadIsMeaningful(http.Header{"Content-Type": []string{"application/json"}}, []byte(`{"id":"resp_1"}`)))
}

func TestCompetitiveResponseWriterCommitsOnlyWinner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	testContext, _ := gin.CreateTestContext(recorder)
	coordinator := newCompetitiveResponseCoordinator(testContext.Writer)
	firstCanceled := false
	secondCanceled := false
	coordinator.setCancelers([]context.CancelFunc{
		func() { firstCanceled = true },
		func() { secondCanceled = true },
	})
	first := newCompetitiveResponseWriter(0, coordinator, nil)
	second := newCompetitiveResponseWriter(1, coordinator, nil)
	first.Header().Set("Content-Type", "text/event-stream")
	second.Header().Set("Content-Type", "text/event-stream")

	_, err := first.Write([]byte(": keepalive\n\n"))
	require.NoError(t, err)
	require.Empty(t, recorder.Body.String())
	_, err = second.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
	require.NoError(t, err)
	require.Empty(t, recorder.Body.String())
	_, err = first.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"))
	require.NoError(t, err)

	require.NotContains(t, recorder.Body.String(), "response.created")
	require.Contains(t, recorder.Body.String(), "response.output_text.delta")
	require.False(t, firstCanceled)
	require.True(t, secondCanceled)
}

func TestCompetitiveCoordinatorWinnerCancelsCandidateSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	testContext, _ := gin.CreateTestContext(recorder)
	coordinator := newCompetitiveResponseCoordinator(testContext.Writer)
	selectionCtx, cancelSelection := context.WithCancel(context.Background())
	defer cancelSelection()
	coordinator.setSelectionCancel(cancelSelection)
	writer := newCompetitiveResponseWriter(0, coordinator, nil)
	writer.Header().Set("Content-Type", "text/event-stream")

	_, err := writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"winner\"}\n\n"))

	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return selectionCtx.Err() == context.Canceled
	}, time.Second, time.Millisecond)
}

func TestAggregateCompetitiveOpenAIUsageUsesExactThenWinnerInputEstimate(t *testing.T) {
	winner := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{
		InputTokens:          100,
		OutputTokens:         20,
		CacheReadInputTokens: 40,
	}}
	candidates := []competitiveOpenAICandidate{
		{account: &service.Account{ID: 1}},
		{account: &service.Account{ID: 2}},
		{account: &service.Account{ID: 3}},
	}
	outcomes := map[int]competitiveOpenAIOutcome{
		0: {id: 0, result: winner},
		1: {id: 1, result: &service.OpenAIForwardResult{Usage: service.OpenAIUsage{InputTokens: 90, OutputTokens: 2}}},
	}

	aggregateCompetitiveOpenAIUsage(winner, 0, candidates, outcomes)

	require.Equal(t, 290, winner.Usage.InputTokens)
	require.Equal(t, 22, winner.Usage.OutputTokens)
	require.Equal(t, 80, winner.Usage.CacheReadInputTokens)
}

func TestRunCompetitiveOpenAIForwardFastInitialSkipsHedgeSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	initialAccount := &service.Account{ID: 41}
	initialSelection := &service.AccountSelectionResult{Account: initialAccount, Acquired: true}
	var selectCalls atomic.Int32
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls.Add(1)
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(_ context.Context, attemptGin *gin.Context, _ *service.Account) (*service.OpenAIForwardResult, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"fast\"}\n\n"))
		return &service.OpenAIForwardResult{}, err
	}

	winner, result, err := (&OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}).runCompetitiveOpenAIForward(
		c,
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"gpt-5",
		"",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, initialAccount.ID, winner.ID)
	require.NotNil(t, result)
	require.Zero(t, selectCalls.Load())
	require.Contains(t, recorder.Body.String(), "fast")
}

func TestRunCompetitiveOpenAIForwardInitialFailureStartsHedgeImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	initialAccount := &service.Account{ID: 51}
	hedgeAccount := &service.Account{ID: 52}
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
	forward := func(_ context.Context, attemptGin *gin.Context, account *service.Account) (*service.OpenAIForwardResult, error) {
		if account.ID == initialAccount.ID {
			initialStarted.Store(true)
			return nil, errors.New("primary failed")
		}
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered\"}\n\n"))
		return &service.OpenAIForwardResult{}, err
	}

	startedAt := time.Now()
	winner, _, err := (&OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}).runCompetitiveOpenAIForward(
		c,
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"gpt-5",
		"",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, hedgeAccount.ID, winner.ID)
	require.False(t, selectedBeforeInitial.Load())
	require.Less(t, time.Since(startedAt), competitiveConcurrencyFirstOutput)
	require.Contains(t, recorder.Body.String(), "recovered")
}

func TestRunCompetitiveOpenAIForwardStartsHedgeOnlyAfterFirstOutputThreshold(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	initialAccount := &service.Account{ID: 61}
	hedgeAccount := &service.Account{ID: 62}
	initialSelection := &service.AccountSelectionResult{Account: initialAccount, Acquired: true}
	var selectedAt atomic.Int64
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		if selectCalls == 1 {
			selectedAt.Store(time.Now().UnixNano())
			return &service.AccountSelectionResult{Account: hedgeAccount, Acquired: true}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(ctx context.Context, attemptGin *gin.Context, account *service.Account) (*service.OpenAIForwardResult, error) {
		attemptGin.Writer.Header().Set("Content-Type", "text/event-stream")
		if account.ID == initialAccount.ID {
			_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
			require.NoError(t, err)
			<-ctx.Done()
			return &service.OpenAIForwardResult{}, ctx.Err()
		}
		_, err := attemptGin.Writer.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hedged\"}\n\n"))
		return &service.OpenAIForwardResult{}, err
	}

	startedAt := time.Now()
	winner, _, err := (&OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}).runCompetitiveOpenAIForward(
		c,
		nil,
		"session",
		initialSelection,
		nil,
		map[int64]struct{}{},
		"gpt-5",
		"",
		selectNext,
		forward,
		zap.NewNop(),
	)

	require.NoError(t, err)
	require.Equal(t, hedgeAccount.ID, winner.ID)
	require.GreaterOrEqual(t, time.Duration(selectedAt.Load()-startedAt.UnixNano()), competitiveConcurrencyFirstOutput)
	require.Less(t, time.Since(startedAt), competitiveConcurrencyFirstOutput+time.Second)
	require.Contains(t, recorder.Body.String(), "hedged")
}

func TestCompetitiveOpenAIFailedBatchUsageCarriesToLaterSuccessExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	accounts := []*service.Account{{ID: 71}, {ID: 72}, {ID: 73}}
	selectCalls := 0
	selectNext := func(_ context.Context, _ map[int64]struct{}) (*service.AccountSelectionResult, error) {
		selectCalls++
		if selectCalls <= 2 {
			return &service.AccountSelectionResult{Account: accounts[selectCalls], Acquired: true}, nil
		}
		return nil, service.ErrNoAvailableAccounts
	}
	forward := func(_ context.Context, _ *gin.Context, account *service.Account) (*service.OpenAIForwardResult, error) {
		switch account.ID {
		case accounts[0].ID:
			return &service.OpenAIForwardResult{Usage: service.OpenAIUsage{
				InputTokens: 11, OutputTokens: 1, ImageInputTokens: 3,
				CacheCreationInputTokens: 5, CacheReadInputTokens: 7, ImageOutputTokens: 9,
			}}, &service.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
		case accounts[1].ID:
			return &service.OpenAIForwardResult{Usage: service.OpenAIUsage{
				InputTokens: 22, OutputTokens: 2, ImageInputTokens: 6,
				CacheCreationInputTokens: 10, CacheReadInputTokens: 14, ImageOutputTokens: 18,
			}}, &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
		default:
			return nil, &service.UpstreamFailoverError{StatusCode: http.StatusGatewayTimeout}
		}
	}

	_, _, err := (&OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}).runCompetitiveOpenAIForward(
		c,
		nil,
		"session",
		&service.AccountSelectionResult{Account: accounts[0], Acquired: true},
		nil,
		map[int64]struct{}{},
		"gpt-5",
		"",
		selectNext,
		forward,
		zap.NewNop(),
	)
	require.Error(t, err)

	success := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{
		InputTokens: 100, OutputTokens: 10, ImageInputTokens: 10,
		CacheCreationInputTokens: 20, CacheReadInputTokens: 30, ImageOutputTokens: 40,
	}}
	mergePendingCompetitiveOpenAIUsage(c, success)
	require.Equal(t, 133, success.Usage.InputTokens)
	require.Equal(t, 13, success.Usage.OutputTokens)
	require.Equal(t, 19, success.Usage.ImageInputTokens)
	require.Equal(t, 35, success.Usage.CacheCreationInputTokens)
	require.Equal(t, 51, success.Usage.CacheReadInputTokens)
	require.Equal(t, 67, success.Usage.ImageOutputTokens)

	mergePendingCompetitiveOpenAIUsage(c, success)
	require.Equal(t, 133, success.Usage.InputTokens)
	require.Equal(t, 13, success.Usage.OutputTokens)
	require.Equal(t, 19, success.Usage.ImageInputTokens)
	require.Equal(t, 35, success.Usage.CacheCreationInputTokens)
	require.Equal(t, 51, success.Usage.CacheReadInputTokens)
	require.Equal(t, 67, success.Usage.ImageOutputTokens)
}
