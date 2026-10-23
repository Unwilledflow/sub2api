package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 本地内容审计拒绝的 403 必须落成 request/client 且计入业务限流，
// 否则会以 internal/platform 的身份进入平台错误率（生产实测 640 行如此）。
func TestClassifyOpsContentPolicyBlockIsClientRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonContentPolicy)

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"api_error",
		"内容审计命中风险规则，请调整输入后重试",
		"",
		http.StatusForbidden,
	)

	require.Equal(t, "request", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "client", errorOwner)
	require.Equal(t, "client_request", errorSource)
}

// 审计拦截判定后必须打上业务限流标记，供 ops 归因使用。
func TestMarkContentPolicyBlockOnlyMarksRejections(t *testing.T) {
	gin.SetMode(gin.TestMode)

	blocked, _ := gin.CreateTestContext(httptest.NewRecorder())
	markContentPolicyBlock(blocked, &securityaudit.Decision{Kind: securityaudit.DecisionBlock, AllowNextStage: false})
	require.True(t, service.HasOpsClientBusinessLimited(blocked))
	require.Equal(t, service.OpsClientBusinessLimitedReasonContentPolicy, service.OpsClientBusinessLimitedReason(blocked))

	allowed, _ := gin.CreateTestContext(httptest.NewRecorder())
	markContentPolicyBlock(allowed, &securityaudit.Decision{Kind: securityaudit.DecisionAllow, AllowNextStage: true})
	require.False(t, service.HasOpsClientBusinessLimited(allowed), "an allowed request must stay out of the business-limited bucket")

	nilDecision, _ := gin.CreateTestContext(httptest.NewRecorder())
	markContentPolicyBlock(nilDecision, nil)
	require.False(t, service.HasOpsClientBusinessLimited(nilDecision))
}
