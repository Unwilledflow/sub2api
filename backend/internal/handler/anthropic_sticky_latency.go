package handler

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	// A slow successful stream should not pin the next logical request to the
	// account that just demonstrated a long first-token delay.
	anthropicSlowStickyThreshold = 5 * time.Second
	anthropicSkipStickyBindKey   = "anthropic_skip_sticky_bind"
)

func anthropicStickyBindAllowed(c *gin.Context) bool {
	if c == nil {
		return true
	}
	skip, _ := c.Get(anthropicSkipStickyBindKey)
	shouldSkip, _ := skip.(bool)
	return !shouldSkip
}

// Keep the old helper name for the competitive-path tests and callers while
// both routing modes share the same request-local bind veto.
func competitiveAnthropicStickyBindAllowed(c *gin.Context) bool {
	return anthropicStickyBindAllowed(c)
}

func (h *GatewayHandler) clearSlowAnthropicStickySession(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	account *service.Account,
	ttft time.Duration,
) (bool, error) {
	if account == nil || ttft < anthropicSlowStickyThreshold {
		return false, nil
	}
	if c != nil {
		c.Set(anthropicSkipStickyBindKey, true)
	}
	if h == nil || h.gatewayService == nil {
		return true, nil
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return true, h.gatewayService.ClearStickySession(ctx, groupID, sessionHash)
}

func (h *GatewayHandler) settleOrdinaryAnthropicSticky(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	account *service.Account,
	result *service.ForwardResult,
	reqLog *zap.Logger,
) {
	if account == nil || !account.IsAnthropic() || result == nil || !result.Stream || result.FirstTokenMs == nil {
		return
	}
	ttft := time.Duration(*result.FirstTokenMs) * time.Millisecond
	slow, clearErr := h.clearSlowAnthropicStickySession(c, groupID, sessionHash, account, ttft)
	if !slow || reqLog == nil {
		return
	}
	if clearErr != nil {
		reqLog.Warn("anthropic.ordinary.slow_sticky_clear_failed",
			zap.Int64("account_id", account.ID),
			zap.Int("first_token_ms", *result.FirstTokenMs),
			zap.Error(clearErr),
		)
		return
	}
	reqLog.Info("anthropic.ordinary.slow_sticky_cleared",
		zap.Int64("account_id", account.ID),
		zap.Int("first_token_ms", *result.FirstTokenMs),
		zap.Duration("threshold", anthropicSlowStickyThreshold),
	)
}
