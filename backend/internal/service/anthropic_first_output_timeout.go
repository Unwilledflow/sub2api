package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

func (s *GatewayService) anthropicFirstOutputTimeout() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.AnthropicFirstOutputTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(s.cfg.Gateway.AnthropicFirstOutputTimeoutSeconds) * time.Second
}

func (s *GatewayService) newAnthropicFirstOutputTimeoutError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	startTime time.Time,
	originalModel string,
	timeout time.Duration,
	responseHeaders http.Header,
) *UpstreamFailoverError {
	elapsed := time.Since(startTime)
	accountID := int64(0)
	accountName := ""
	platform := ""
	if account != nil {
		accountID = account.ID
		accountName = account.Name
		platform = account.Platform
	}
	logger.LegacyPrintf(
		"service.gateway",
		"Anthropic first output timeout: account=%d model=%s elapsed=%s limit=%s",
		accountID, originalModel, elapsed, timeout,
	)
	requestID := ""
	if responseHeaders != nil {
		requestID = responseHeaders.Get("x-request-id")
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: platform, AccountID: accountID, AccountName: accountName,
		UpstreamStatusCode: http.StatusGatewayTimeout, UpstreamRequestID: requestID,
		Kind: "first_output_timeout", Message: "Anthropic upstream produced no semantic output before the deadline",
		Detail: fmt.Sprintf("elapsed_ms=%d timeout_ms=%d", elapsed.Milliseconds(), timeout.Milliseconds()),
	})
	if s != nil && s.rateLimitService != nil && account != nil {
		s.rateLimitService.HandleStreamTimeout(ctx, account, originalModel)
	}
	var headers http.Header
	if responseHeaders != nil {
		headers = responseHeaders.Clone()
	}
	return &UpstreamFailoverError{
		StatusCode:               http.StatusGatewayTimeout,
		ResponseBody:             []byte(`{"type":"error","error":{"type":"first_output_timeout","message":"Upstream produced no output before the deadline"}}`),
		ResponseHeaders:          headers,
		SafeToFailoverAfterWrite: true,
	}
}
