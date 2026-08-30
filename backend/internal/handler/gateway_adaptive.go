package handler

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const ginKeyGatewayAdaptiveSession = "gateway_adaptive_session"

// gatewayAdaptiveSession drives leaf-group order for Anthropic/Gemini Adaptive parents.
// Billing HOLD is optional (OpenAI path uses full AdaptiveBilling); here we only
// reorder leaf groups and let normal per-request billing run on the active leaf.
type gatewayAdaptiveSession struct {
	ParentGroupID  int64
	Plan           *service.AdaptiveRoutePlan
	LeafGroupIDs   []int64
	LeafIndex      int
	CanonicalModel string
}

func getGatewayAdaptiveSession(c *gin.Context) *gatewayAdaptiveSession {
	if c == nil {
		return nil
	}
	v, ok := c.Get(ginKeyGatewayAdaptiveSession)
	if !ok || v == nil {
		return nil
	}
	s, _ := v.(*gatewayAdaptiveSession)
	return s
}

// startGatewayAdaptiveIfParent plans Adaptive leaves when the key's group is an
// Adaptive parent for Anthropic or Gemini. Returns the API key bound to the
// first usable leaf (or original key if not Adaptive).
func (h *GatewayHandler) startGatewayAdaptiveIfParent(
	ctx context.Context,
	c *gin.Context,
	apiKey *service.APIKey,
	requestedModel string,
	protocol service.AdaptiveRouteProtocol,
	reqLog *zap.Logger,
) *service.APIKey {
	if h == nil || apiKey == nil || apiKey.GroupID == nil {
		return apiKey
	}
	if h.cfg == nil || !h.cfg.Gateway.AdaptiveRoutingEnabled {
		return apiKey
	}
	if h.adaptivePlanner == nil {
		return apiKey
	}
	platform := ""
	if apiKey.Group != nil {
		platform = apiKey.Group.Platform
	}
	if platform != service.PlatformAnthropic && platform != service.PlatformGemini {
		// Still install Anti-Stall for non-Adaptive Anthropic/Gemini keys below via caller.
		return apiKey
	}

	routeMode := service.AdaptiveRouteModeFromPreference(apiKey.AdaptiveRoutingPreference)
	plan, err := h.adaptivePlanner.Plan(ctx, service.AdaptiveRouteRequest{
		ParentGroupID:     *apiKey.GroupID,
		Platform:          platform,
		RequestedModel:    requestedModel,
		Mode:              routeMode,
		MaxRateMultiplier: apiKey.AdaptiveMaxRateMultiplier,
		Protocol:          protocol,
	})
	if err != nil {
		if errors.Is(err, service.ErrAdaptivePoolNotFound) || errors.Is(err, service.ErrAdaptivePoolDisabled) {
			return apiKey
		}
		if reqLog != nil {
			reqLog.Warn("gateway.adaptive_plan_failed",
				zap.Int64("parent_group_id", *apiKey.GroupID),
				zap.String("platform", platform),
				zap.Error(err),
			)
		}
		return apiKey
	}
	if plan == nil || plan.CandidateCount() == 0 {
		return apiKey
	}

	leafIDs := make([]int64, 0, plan.CandidateCount())
	var firstKey *service.APIKey
	for _, cand := range plan.Candidates() {
		group, gerr := h.apiKeyService.ResolveGroupByID(ctx, cand.LeafGroupID)
		if gerr != nil || group == nil {
			continue
		}
		leafIDs = append(leafIDs, group.ID)
		if firstKey == nil {
			firstKey = cloneAPIKeyWithGroup(apiKey, group)
		}
	}
	if len(leafIDs) == 0 || firstKey == nil {
		return apiKey
	}

	session := &gatewayAdaptiveSession{
		ParentGroupID:  *apiKey.GroupID,
		Plan:           plan,
		LeafGroupIDs:   leafIDs,
		LeafIndex:      0,
		CanonicalModel: plan.CanonicalModel,
	}
	if c != nil {
		c.Set(ginKeyGatewayAdaptiveSession, session)
	}
	if reqLog != nil {
		reqLog.Info("gateway.adaptive_plan_ready",
			zap.Int64("parent_group_id", session.ParentGroupID),
			zap.String("platform", platform),
			zap.String("model", session.CanonicalModel),
			zap.String("route_mode", string(plan.Mode)),
			zap.Int64s("leaf_order", leafIDs),
			zap.String("anti_stall_tier", apiKey.AntiStallTier),
		)
	}
	return firstKey
}

// advanceGatewayAdaptiveLeaf switches to the next Adaptive leaf group.
// Returns the new leaf-bound API key, or nil if no more leaves.
func (h *GatewayHandler) advanceGatewayAdaptiveLeaf(
	ctx context.Context,
	c *gin.Context,
	rootKey *service.APIKey,
	reqLog *zap.Logger,
) *service.APIKey {
	session := getGatewayAdaptiveSession(c)
	if session == nil || rootKey == nil || h == nil || h.apiKeyService == nil {
		return nil
	}
	next := session.LeafIndex + 1
	if next >= len(session.LeafGroupIDs) {
		return nil
	}
	// Anti-Stall leaf budget
	if anti := service.AntiStallSessionFromGin(c); anti != nil && anti.Config().Enabled {
		if anti.LeafSwitches() >= anti.Config().MaxLeafSwitches {
			return nil
		}
		anti.RecordLeafSwitch()
		service.EnsureAntiStallDripRunning(c, anti)
	}
	session.LeafIndex = next
	gid := session.LeafGroupIDs[next]
	group, err := h.apiKeyService.ResolveGroupByID(ctx, gid)
	if err != nil || group == nil {
		if reqLog != nil {
			reqLog.Warn("gateway.adaptive_resolve_leaf_failed", zap.Int64("leaf_group_id", gid), zap.Error(err))
		}
		return nil
	}
	if reqLog != nil {
		reqLog.Warn("gateway.adaptive_leaf_switch",
			zap.Int64("parent_group_id", session.ParentGroupID),
			zap.Int64("leaf_group_id", gid),
			zap.Int("leaf_index", next),
		)
	}
	return cloneAPIKeyWithGroup(rootKey, group)
}
