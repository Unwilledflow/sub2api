package handler

import (
	"context"
	"time"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestOrdinaryAnthropicSlowStreamClearsStickyAndSkipsRebind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const sessionHash = "ordinary-slow-session"
	groupID := int64(97)
	account := &service.Account{ID: 2286, Platform: service.PlatformAnthropic}
	cache := &competitiveAnthropicStickyCache{bindings: map[string]int64{sessionHash: account.ID}}
	h := &GatewayHandler{gatewayService: newCompetitiveAnthropicGatewayServiceForTest(cache)}
	firstTokenMs := 5_000

	h.settleOrdinaryAnthropicSticky(c, &groupID, sessionHash, account, &service.ForwardResult{
		Stream:       true,
		FirstTokenMs: &firstTokenMs,
	}, zap.NewNop())
	if anthropicStickyBindAllowed(c) {
		require.NoError(t, h.gatewayService.BindStickySession(c.Request.Context(), &groupID, sessionHash, account.ID))
	}

	require.NotContains(t, cache.bindings, sessionHash)
	require.Equal(t, 1, cache.deleteCalls)
	require.Zero(t, cache.setCalls)
	require.False(t, anthropicStickyBindAllowed(c))
}

func TestOrdinaryAnthropicFastStreamKeepsSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	const sessionHash = "ordinary-fast-session"
	groupID := int64(97)
	account := &service.Account{ID: 2288, Platform: service.PlatformAnthropic}
	cache := &competitiveAnthropicStickyCache{bindings: make(map[string]int64)}
	h := &GatewayHandler{gatewayService: newCompetitiveAnthropicGatewayServiceForTest(cache)}
	firstTokenMs := 4_999

	h.settleOrdinaryAnthropicSticky(c, &groupID, sessionHash, account, &service.ForwardResult{
		Stream:       true,
		FirstTokenMs: &firstTokenMs,
	}, zap.NewNop())
	if anthropicStickyBindAllowed(c) {
		require.NoError(t, h.gatewayService.BindStickySession(c.Request.Context(), &groupID, sessionHash, account.ID))
	}

	require.Equal(t, account.ID, cache.bindings[sessionHash])
	require.Zero(t, cache.deleteCalls)
	require.Equal(t, 1, cache.setCalls)
	require.True(t, anthropicStickyBindAllowed(c))
}

func (c *competitiveAnthropicStickyCache) ClaimGrokVideoBilled(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	return false, nil
}

func (c *competitiveAnthropicStickyCache) GetGrokVideoPendingBilling(ctx context.Context, key string) ([]byte, error) {
	return nil, nil
}

func (c *competitiveAnthropicStickyCache) GetReasoningContent(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (c *competitiveAnthropicStickyCache) SetReasoningContent(ctx context.Context, itemID string, content string, ttl time.Duration) error {
	return nil
}
func (c *competitiveAnthropicStickyCache) SetGrokVideoPendingBilling(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return nil
}
func (c *competitiveAnthropicStickyCache) GetGrokVideoBilled(ctx context.Context, key string) ([]byte, error) {
	return nil, nil
}

func (c *competitiveAnthropicStickyCache) ReleaseGrokVideoBilled(ctx context.Context, key string) error {
	return nil
}
