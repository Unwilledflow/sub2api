package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestDefaultImportedAccountPriorityStaysWithinSchedulingBounds(t *testing.T) {
	require.NoError(t, service.ValidateAccountPriority(defaultImportedAccountPriority))
	require.Equal(t, service.AccountPriorityMax, defaultImportedAccountPriority)
}

func TestCreateAccountPriorityDefaultsOnlyWhenOmitted(t *testing.T) {
	priority := 1
	require.Equal(t, service.AccountPriorityMax, createAccountPriority(nil))
	require.Equal(t, priority, createAccountPriority(&priority))
}

func TestAccountManagementRejectsOutOfRangeSchedulingValues(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		mount  func(*gin.Engine, *AccountHandler)
	}{
		{
			name:   "create priority",
			method: http.MethodPost,
			path:   "/accounts",
			body:   `{"name":"account","platform":"openai","type":"apikey","credentials":{"api_key":"test"},"priority":31}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.POST("/accounts", handler.Create) },
		},
		{
			name:   "update load factor",
			method: http.MethodPut,
			path:   "/accounts/1",
			body:   `{"load_factor":1001}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.PUT("/accounts/:id", handler.Update) },
		},
		{
			name:   "bulk update priority",
			method: http.MethodPost,
			path:   "/accounts/bulk-update",
			body:   `{"account_ids":[1],"priority":-1}`,
			mount: func(router *gin.Engine, handler *AccountHandler) {
				router.POST("/accounts/bulk-update", handler.BulkUpdate)
			},
		},
		{
			name:   "batch create load factor",
			method: http.MethodPost,
			path:   "/accounts/batch",
			body:   `{"accounts":[{"name":"account","platform":"openai","type":"apikey","credentials":{"api_key":"test"},"load_factor":1001}]}`,
			mount:  func(router *gin.Engine, handler *AccountHandler) { router.POST("/accounts/batch", handler.BatchCreate) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			stub := newStubAdminService()
			handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			tt.mount(router, handler)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Empty(t, stub.createdAccounts)
			require.Zero(t, stub.updateAccountCalls)
		})
	}
}

func TestAccountManagementAllowsClearLoadFactorButRejectsZeroPriority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := newStubAdminService()
	handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.PUT("/accounts/:id", handler.Update)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/accounts/1", strings.NewReader(`{"priority":0,"load_factor":0}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, stub.updateAccountCalls)
}
