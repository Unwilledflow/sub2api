package handler

// /v1/messages 调度闸门回归。
//
// 历史背景：sanitizeGroupMessagesDispatchFields 对非 openai/composite 平台强制
// AllowMessagesDispatch=false，曾让 CN 分组恒 403，故补了平台豁免。闸门最初存在
// 是因为 /v1/messages 走原生 Anthropic 直通，只对开放该能力的组合有意义。
//
// 普通分组仍受 AllowMessagesDispatch 约束；以下用例同时锁定 Grok/CN、
// Composite、Adaptive 及已解析多候选目标的平台豁免。

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAllowOpenAICompatibleMessagesDispatch_CNProvidersExempt(t *testing.T) {
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, nil), "无 key 保持放行")

	for _, platform := range []string{service.PlatformCN, service.PlatformVideo, service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek, service.PlatformGrok} {
		apiKey := &service.APIKey{Group: &service.Group{Platform: platform, AllowMessagesDispatch: false}}
		require.True(t, allowOpenAICompatibleMessagesDispatch(nil, apiKey),
			"%s 分组必须豁免 allow_messages_dispatch 闸门", platform)
	}

	// 普通 OpenAI 分组关闭开关后拒绝；打开后放行。
	openaiOff := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}}
	require.False(t, allowOpenAICompatibleMessagesDispatch(nil, openaiOff),
		"openai 分组关闭 allow_messages_dispatch 后必须拒绝")
	openaiOn := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: true}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, openaiOn))

	namedGrok := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, Name: "Grok分组", AllowMessagesDispatch: false}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil, namedGrok),
		"OpenAI 平台但名叫 Grok 的分组必须放行 Claude Code /v1/messages")
}

func TestAllowOpenAICompatibleMessagesDispatch_OpenAIGroupResolvedGrok(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformGrok))
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}}
	require.True(t, allowOpenAICompatibleMessagesDispatch(c, apiKey),
		"Claude Code 打 grok-* 时，OpenAI 平台的 Grok 分组不能再 403")
}

func TestAllowOpenAICompatibleMessagesDispatch_CompositeResolvedTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCompositeCtx := func(model string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}}
		ensureCompositeTargetPlatform(c, apiKey, model)
		return c
	}

	// composite 分组不再受开关约束：无论解析到哪个目标都放行。
	for _, model := range []string{"grok-4.3", "kimi-k2-thinking", "glm-5.2", "deepseek-v3.2", "gpt-5.5"} {
		c := newCompositeCtx(model)
		require.True(t, allowOpenAICompatibleMessagesDispatch(c, &service.APIKey{
			Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false},
		}), "model=%s", model)
	}

	// 未解析出目标平台也放行，闸门不再是 403 来源。
	cNone, _ := gin.CreateTestContext(httptest.NewRecorder())
	cNone.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	require.True(t, allowOpenAICompatibleMessagesDispatch(cNone,
		&service.APIKey{Group: &service.Group{Platform: service.PlatformComposite, AllowMessagesDispatch: false}}))
}

func TestAllowOpenAICompatibleMessagesDispatch_AdaptiveParentExempt(t *testing.T) {
	parentID := int64(108)
	h := &OpenAIGatewayHandler{
		adaptivePlanner: service.NewAdaptiveRoutePlanner(
			stubAdaptivePoolRepo{snapshot: &service.AdaptivePoolSnapshot{
				ParentGroupID: parentID,
				Platform:      service.PlatformOpenAI,
				Enabled:       true,
				Members: []service.AdaptiveLeafRef{{
					LeafGroupID: 118,
					Enabled:     true,
					SortOrder:   1,
				}},
			}},
			nil,
			stubAdaptiveGroupRepo{groups: map[int64]*service.Group{
				118: {ID: 118, Platform: service.PlatformAnthropic, Status: service.StatusActive},
			}},
			nil, nil,
		),
		cfg: &config.Config{Gateway: config.GatewayConfig{AdaptiveRoutingEnabled: true}},
	}
	openaiIdentity := &service.APIKey{
		GroupID: &parentID,
		Group: &service.Group{
			ID:                    parentID,
			Platform:              service.PlatformOpenAI,
			AllowMessagesDispatch: false,
		},
	}
	require.True(t, h.allowOpenAICompatibleMessagesDispatch(nil, openaiIdentity),
		"legacy Adaptive parent still openai-identity must not 403 /v1/messages")

	adaptiveIdentity := &service.APIKey{
		GroupID: &parentID,
		Group: &service.Group{
			ID:                    parentID,
			Platform:              service.PlatformAdaptive,
			AllowMessagesDispatch: false,
		},
	}
	require.True(t, h.allowOpenAICompatibleMessagesDispatch(nil, adaptiveIdentity),
		"Adaptive inbound platform must not 403 /v1/messages")

	plain := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: false}}
	require.False(t, h.allowOpenAICompatibleMessagesDispatch(nil, plain),
		"plain OpenAI group still requires allow_messages_dispatch")

	emptyPool := &OpenAIGatewayHandler{
		adaptivePlanner: service.NewAdaptiveRoutePlanner(
			stubAdaptivePoolRepo{snapshot: &service.AdaptivePoolSnapshot{
				ParentGroupID: parentID,
				Platform:      service.PlatformAdaptive,
				Enabled:       true,
			}},
			nil, nil, nil, nil,
		),
		cfg: &config.Config{Gateway: config.GatewayConfig{AdaptiveRoutingEnabled: true}},
	}
	require.True(t, emptyPool.allowOpenAICompatibleMessagesDispatch(nil, adaptiveIdentity),
		"Adaptive inbound platform stays exempt even with an empty pool")
	require.False(t, emptyPool.allowOpenAICompatibleMessagesDispatch(nil, openaiIdentity),
		"enabled Adaptive parent still labeled openai with zero leaves follows the OpenAI switch")
}

type stubAdaptivePoolRepo struct {
	snapshot *service.AdaptivePoolSnapshot
}

func (s stubAdaptivePoolRepo) GetAdaptivePoolSnapshot(context.Context, int64) (*service.AdaptivePoolSnapshot, error) {
	return s.snapshot, nil
}

type stubAdaptiveGroupRepo struct {
	groups map[int64]*service.Group
}

func (s stubAdaptiveGroupRepo) GetByID(_ context.Context, groupID int64) (*service.Group, error) {
	return s.groups[groupID], nil
}

func TestAdaptiveLeafPlatformMatches(t *testing.T) {
	require.True(t, adaptiveLeafPlatformMatches(service.PlatformAnthropic, service.PlatformAnthropic))
	require.True(t, adaptiveLeafPlatformMatches(service.PlatformOpenAI, service.PlatformOpenAI))
	require.True(t, adaptiveLeafPlatformMatches(service.PlatformKimi, service.PlatformCN))
	require.True(t, adaptiveLeafPlatformMatches(service.PlatformGrok, service.PlatformGrok))
	require.True(t, adaptiveLeafPlatformMatches(service.PlatformAntigravity, service.PlatformGemini))
	require.False(t, adaptiveLeafPlatformMatches(service.PlatformCN, service.PlatformOpenAI))
	require.False(t, adaptiveLeafPlatformMatches(service.PlatformVideo, service.PlatformOpenAI))
	require.False(t, adaptiveLeafPlatformMatches(service.PlatformAnthropic, service.PlatformOpenAI))
	require.False(t, adaptiveLeafPlatformMatches(service.PlatformGemini, service.PlatformGrok))
}

func TestPickAdaptiveMatchingLeafPrefersWantedProtocol(t *testing.T) {
	leaves := []service.AdaptiveLeafIdentity{
		{GroupID: 118, Platform: service.PlatformAnthropic},
		{GroupID: 115, Platform: service.PlatformCN},
		{GroupID: 33, Platform: service.PlatformOpenAI},
		{GroupID: 129, Platform: service.PlatformGrok},
		{GroupID: 114, Platform: service.PlatformGemini},
	}

	openaiLeaf, ok := pickAdaptiveMatchingLeaf(leaves, []string{service.PlatformOpenAI})
	require.True(t, ok)
	require.Equal(t, int64(33), openaiLeaf.GroupID)

	grokLeaf, ok := pickAdaptiveMatchingLeaf(leaves, []string{service.PlatformGrok, service.PlatformVideo})
	require.True(t, ok)
	require.Equal(t, int64(129), grokLeaf.GroupID)

	geminiLeaf, ok := pickAdaptiveMatchingLeaf(leaves, []string{service.PlatformGemini})
	require.True(t, ok)
	require.Equal(t, int64(114), geminiLeaf.GroupID)

	_, ok = pickAdaptiveMatchingLeaf(leaves, []string{service.PlatformVideo})
	require.False(t, ok)
}

// composite 解析到 grok/CN 目标时，Group 级调度映射（gpt-5.x 默认值为 openai
// 专属）不得注入，模型改写完全交给账号级 model_mapping。
func TestResolveOpenAIMessagesDispatchMappedModel_CompositeCNTargetsSkipGroupMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, model := range []string{"kimi-k2-thinking", "glm-5.2", "deepseek-v3.2", "grok-4.3"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}
		ensureCompositeTargetPlatform(c, apiKey, model)

		require.Empty(t, resolveOpenAIMessagesDispatchMappedModel(c, apiKey, "claude-sonnet-4-5-20250929"), "model=%s", model)
	}
}

func TestAllowOpenAICompatibleMessagesDispatch_SmartRoutingResolvedOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	primaryID := int64(1)
	secondID := int64(2)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{
		GroupID:       &primaryID,
		RouteGroupIDs: []int64{primaryID, secondID},
		Group:         &service.Group{ID: primaryID, Platform: service.PlatformAnthropic, AllowMessagesDispatch: false},
	}
	// Simulate an already-resolved smart-route context. Composite-only target
	// resolution is a no-op for this Anthropic primary and cannot set it.
	c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformOpenAI))
	require.True(t, allowOpenAICompatibleMessagesDispatch(c, apiKey),
		"resolved OpenAI smart-route target must be eligible for /v1/messages")

	plain := &service.APIKey{
		Group: &service.Group{ID: primaryID, Platform: service.PlatformAnthropic, AllowMessagesDispatch: false},
	}
	require.False(t, allowOpenAICompatibleMessagesDispatch(c, plain),
		"ordinary single-group key still requires allow_messages_dispatch even with a resolved OpenAI context")
}
