//go:build unit

package admin

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountTypeBindingAllowsCloudflare(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createBody := `{"name":"cf","platform":"openai","type":"cloudflare","credentials":{"api_key":"token","account_id":"df9b7a01eff429b0ecaeeea0366cde12"}}`
	createCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	createCtx.Request = httptest.NewRequest("POST", "/", bytes.NewBufferString(createBody))
	createCtx.Request.Header.Set("Content-Type", "application/json")
	var createReq CreateAccountRequest
	require.NoError(t, createCtx.ShouldBindJSON(&createReq))
	require.Equal(t, "cloudflare", createReq.Type)

	updateCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	updateCtx.Request = httptest.NewRequest("PUT", "/", bytes.NewBufferString(`{"type":"cloudflare"}`))
	updateCtx.Request.Header.Set("Content-Type", "application/json")
	var updateReq UpdateAccountRequest
	require.NoError(t, updateCtx.ShouldBindJSON(&updateReq))
	require.Equal(t, "cloudflare", updateReq.Type)
}

func TestUpdateAccountRequestDecodesSnakeCaseAccountGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := `{"group_ids":[17,142],"account_groups":[{"account_id":2624,"group_id":17,"priority":1},{"account_id":2624,"group_id":142,"priority":2}]}`
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("PUT", "/", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	var req UpdateAccountRequest
	require.NoError(t, ctx.ShouldBindJSON(&req))
	require.NotNil(t, req.GroupIDs)
	require.Equal(t, []int64{17, 142}, *req.GroupIDs)
	require.Len(t, req.AccountGroups, 2)
	require.Equal(t, int64(17), req.AccountGroups[0].GroupID)
	require.Equal(t, int64(142), req.AccountGroups[1].GroupID)
	require.Equal(t, 1, req.AccountGroups[0].Priority)
	require.Equal(t, 2, req.AccountGroups[1].Priority)
}
