package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type oauthBatchDeleteAdminStub struct {
	service.AdminService
	accounts map[int64]*service.Account
	deleted  []int64
}

func (s *oauthBatchDeleteAdminStub) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	result := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account := s.accounts[id]; account != nil {
			result = append(result, account)
		}
	}
	return result, nil
}

func (s *oauthBatchDeleteAdminStub) DeleteAccount(_ context.Context, id int64) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func TestBatchDeleteOpenAIOAuth_DeduplicatesRestrictsAndDeletesChildrenFirst(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parentID := int64(1)
	stub := &oauthBatchDeleteAdminStub{accounts: map[int64]*service.Account{
		1: {ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth},
		2: {ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, ParentAccountID: &parentID},
		3: {ID: 3, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey},
		4: {ID: 4, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth},
	}}
	handler := &AccountHandler{adminService: stub}
	router := gin.New()
	router.POST("/batch-delete", handler.BatchDeleteOpenAIOAuth)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/batch-delete", bytes.NewBufferString(`{"account_ids":[1,2,3,4,999,1]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{2, 1}, stub.deleted)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Total      int     `json:"total"`
			Deleted    int     `json:"deleted"`
			DeletedIDs []int64 `json:"deleted_ids"`
			Skipped    []struct {
				AccountID int64 `json:"account_id"`
			} `json:"skipped"`
			Failed []struct {
				AccountID int64 `json:"account_id"`
			} `json:"failed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Zero(t, body.Code)
	require.Equal(t, 5, body.Data.Total)
	require.Equal(t, 2, body.Data.Deleted)
	require.Equal(t, []int64{2, 1}, body.Data.DeletedIDs)
	require.Len(t, body.Data.Skipped, 2)
	require.Len(t, body.Data.Failed, 1)
	require.Equal(t, int64(999), body.Data.Failed[0].AccountID)
}
