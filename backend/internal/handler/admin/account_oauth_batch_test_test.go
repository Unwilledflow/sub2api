package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type oauthBatchTestAdminStub struct {
	service.AdminService
	accounts map[int64]*service.Account
}

func (s *oauthBatchTestAdminStub) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	result := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account := s.accounts[id]; account != nil {
			result = append(result, account)
		}
	}
	return result, nil
}

type oauthBatchTestRunnerStub struct {
	mu      sync.Mutex
	calls   []int64
	results map[int64]*service.ScheduledTestResult
}

func (s *oauthBatchTestRunnerStub) RunTestBackground(_ context.Context, accountID int64, _ string) (*service.ScheduledTestResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, accountID)
	s.mu.Unlock()
	return s.results[accountID], nil
}

func TestBatchTestOpenAIOAuth_DeduplicatesRestrictsAndPreservesOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminStub := &oauthBatchTestAdminStub{accounts: map[int64]*service.Account{
		1: {ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		2: {ID: 2, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth},
		3: {ID: 3, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
	}}
	runner := &oauthBatchTestRunnerStub{results: map[int64]*service.ScheduledTestResult{
		1: {Status: "success", LatencyMs: 120},
		3: {Status: "failed", ErrorMessage: "upstream rejected", LatencyMs: 75},
	}}
	handler := &AccountHandler{adminService: adminStub, backgroundTestRunner: runner}
	router := gin.New()
	router.POST("/batch-test", handler.BatchTestOpenAIOAuth)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/batch-test", bytes.NewBufferString(`{"account_ids":[3,1,2,999,1]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Data struct {
			Total   int `json:"total"`
			Success int `json:"success"`
			Failed  int `json:"failed"`
			Skipped int `json:"skipped"`
			Results []struct {
				AccountID int64  `json:"account_id"`
				Success   bool   `json:"success"`
				Skipped   bool   `json:"skipped"`
				Error     string `json:"error"`
			} `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, 4, body.Data.Total)
	require.Equal(t, 1, body.Data.Success)
	require.Equal(t, 2, body.Data.Failed)
	require.Equal(t, 1, body.Data.Skipped)
	require.Equal(t, []int64{3, 1, 2, 999}, []int64{
		body.Data.Results[0].AccountID,
		body.Data.Results[1].AccountID,
		body.Data.Results[2].AccountID,
		body.Data.Results[3].AccountID,
	})
	require.True(t, body.Data.Results[1].Success)
	require.True(t, body.Data.Results[2].Skipped)
	require.Contains(t, body.Data.Results[3].Error, "not found")
	sort.Slice(runner.calls, func(i, j int) bool { return runner.calls[i] < runner.calls[j] })
	require.Equal(t, []int64{1, 3}, runner.calls)
}
