package handler

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const ginKeyGatewayAdaptiveSession = "gateway_adaptive_session"

// SetAdaptivePlanner attaches Adaptive route planner. Leaf groups may span
// platforms; the parent row's platform is only plan identity, not a filter.
func (h *GatewayHandler) SetAdaptivePlanner(planner *service.AdaptiveRoutePlanner) {
	if h != nil {
		h.adaptivePlanner = planner
	}
}

// gatewayAdaptiveSession drives leaf-group order for Anthropic/Gemini Adaptive parents.
// When Billing is set, HOLD replaces v247 text preauth and capture runs on RecordUsage.
type gatewayAdaptiveSession struct {
	ParentGroupID  int64
	Plan           *service.AdaptiveRoutePlan
	Billing        *service.AdaptiveBillingContext
	LeafGroupIDs   []int64
	LeafIndex      int
	CanonicalModel string
	UsageSubmitted bool
	// LeafSessionKey identifies the conversation for leaf stickiness. Empty when
	// the client carries no session signal, in which case stickiness is skipped
	// rather than guessed.
	LeafSessionKey string
	// StickyLeafFresh is true once the remembered leaf has been replaced by the
	// leaf this request actually bound, so the write happens once per request.
	StickyLeafFresh bool
}

type gatewayAdaptiveSessionContextKey struct{}

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

// adaptiveLeafSessionKey derives the identity used to remember which adaptive
// leaf a conversation is using. Claude Code's explicit session id is the most
// stable boundary; callers without one fall back to the generic client session
// hash so at least the usual client still gets stickiness.
func (h *GatewayHandler) adaptiveLeafSessionKey(c *gin.Context, apiKey *service.APIKey) string {
	if c == nil || apiKey == nil {
		return ""
	}
	if claudeSessionID := service.ClaudeCodeSessionIDFromHeader(c); claudeSessionID != "" {
		return service.DeriveSessionHashFromSeed(claudeSessionID)
	}
	return service.ExtractClientSessionID(c)
}

// promoteAdaptiveLeaf moves leafID to the front of ids, preserving the relative
// order of everything else. Returns ids unchanged when leafID is not present, so
// a stale remembered leaf can never introduce a candidate the planner excluded
// (it may have become unavailable, over budget, or out of the user's allowlist).
func promoteAdaptiveLeaf(ids []int64, leafID int64) []int64 {
	idx := -1
	for i, id := range ids {
		if id == leafID {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return ids
	}
	out := make([]int64, 0, len(ids))
	out = append(out, leafID)
	out = append(out, ids[:idx]...)
	out = append(out, ids[idx+1:]...)
	return out
}

// startGatewayAdaptiveIfParent plans Adaptive leaves when the key's group is an
// Adaptive parent. Leaves may be OpenAI / Anthropic / Gemini / CN. Returns the
// API key bound to the first usable leaf (or original key if not Adaptive).
func (h *GatewayHandler) startGatewayAdaptiveIfParent(
	ctx context.Context,
	c *gin.Context,
	apiKey *service.APIKey,
	requestedModel string,
	protocol service.AdaptiveRouteProtocol,
	body []byte,
	reqLog *zap.Logger,
) *service.APIKey {
	if h == nil || apiKey == nil || apiKey.GroupID == nil || apiKey.User == nil {
		return apiKey
	}
	if isAdaptiveParentAPIKey(apiKey) {
		bindAdaptiveExecutionScope(c, apiKey.Group, nil)
	}
	if h.cfg == nil || !h.cfg.Gateway.AdaptiveRoutingEnabled {
		return apiKey
	}
	if h.adaptivePlanner == nil || h.adaptiveBilling == nil {
		return apiKey
	}
	platform := ""
	if apiKey.Group != nil && apiKey.Group.Platform != service.PlatformAdaptive {
		platform = apiKey.Group.Platform
	}
	if platform == "" {
		platform = service.PlatformOpenAI
	}

	routeMode := service.AdaptiveRouteModeFromPreference(apiKey.AdaptiveRoutingPreference)
	allowedGroupIDs := map[int64]struct{}{}
	if apiKey.User != nil {
		allowedGroupIDs, _ = h.apiKeyService.GetUserAllowedGroupIDSet(ctx, apiKey.User.ID)
	}
	requiredCapability, requireCompact := adaptiveRequestRequirements(c, protocol, body)
	plan, err := h.adaptivePlanner.Plan(ctx, service.AdaptiveRouteRequest{
		ParentGroupID:            *apiKey.GroupID,
		UserID:                   apiKey.User.ID,
		UserIsAdmin:              apiKey.User.IsAdmin(),
		AllowedGroupIDs:          allowedGroupIDs,
		Platform:                 platform,
		RequestedModel:           requestedModel,
		Mode:                     routeMode,
		MaxRateMultiplier:        apiKey.AdaptiveMaxRateMultiplier,
		AllowedLeafGroupIDs:      service.AdaptiveLeafAllowlist(apiKey.AdaptiveLeafGroupIDs),
		Protocol:                 protocol,
		RequiredOpenAICapability: requiredCapability,
		RequireCompact:           requireCompact,
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
	// A leaf whose upstream wire format differs from the client's can only serve
	// this request through cross-protocol conversion. When that leaf has the
	// switch off, choosing it guarantees a 400 and burns a pool switch for
	// nothing — and because the provider-side prompt cache is scoped to the
	// upstream credential, switching pools also throws the caller's cache away.
	//
	// Such leaves are demoted to the back of the candidate order rather than
	// dropped: a compatible leaf is preferred (no 400, cache preserved), but if
	// none exists the original order still applies and the forwarding layer
	// produces its usual, diagnosable cross-protocol error.
	inboundFamily := service.InboundProtocolFamily(GetInboundEndpoint(c))
	compatible := make([]int64, 0, plan.CandidateCount())
	var compatibleFirstKey *service.APIKey
	demoted := make([]int64, 0)
	var demotedFirstKey *service.APIKey
	for _, cand := range plan.Candidates() {
		group, gerr := h.apiKeyService.ResolveGroupByID(ctx, cand.LeafGroupID)
		if gerr != nil || group == nil {
			continue
		}
		if service.GroupCanServeInboundFamily(inboundFamily, group.Platform, group.CrossProtocolConversionEnabled) {
			compatible = append(compatible, group.ID)
			if compatibleFirstKey == nil {
				compatibleFirstKey = cloneAPIKeyWithGroup(apiKey, group)
			}
			continue
		}
		if reqLog != nil {
			reqLog.Info("gateway.adaptive_leaf_demoted_protocol_mismatch",
				zap.Int64("leaf_group_id", group.ID),
				zap.String("leaf_platform", group.Platform),
				zap.String("inbound_family", string(inboundFamily)),
			)
		}
		demoted = append(demoted, group.ID)
		if demotedFirstKey == nil {
			demotedFirstKey = cloneAPIKeyWithGroup(apiKey, group)
		}
	}

	// Leaf stickiness: the leaf this session used last time goes first.
	//
	// The planner re-derives its order on every request from rate multiplier and
	// health scores, and has no memory of the previous request. A small health
	// wobble was therefore enough to move a session onto a different upstream
	// pool — and the provider-side prompt cache is scoped to the upstream
	// credential, so the whole context was re-read at full input price.
	//
	// This only reorders. If the remembered leaf is gone, or cannot serve this
	// request, the planned order still applies unchanged.
	if sessionKey := h.adaptiveLeafSessionKey(c, apiKey); sessionKey != "" && h.gatewayService != nil {
		if preferred := h.gatewayService.PreferredAdaptiveLeaf(ctx, *apiKey.GroupID, sessionKey); preferred > 0 {
			headBefore := int64(0)
			if len(compatible) > 0 {
				headBefore = compatible[0]
			}
			compatible = promoteAdaptiveLeaf(compatible, preferred)
			// Re-bind the first key only when the promotion actually changed the
			// head; otherwise the planned head already wins and its key stands.
			if len(compatible) > 0 && compatible[0] == preferred && headBefore != preferred {
				if group, gerr := h.apiKeyService.ResolveGroupByID(ctx, preferred); gerr == nil && group != nil {
					compatibleFirstKey = cloneAPIKeyWithGroup(apiKey, group)
				}
			}
			if reqLog != nil {
				reqLog.Debug("gateway.adaptive_leaf_sticky_preferred",
					zap.Int64("parent_group_id", *apiKey.GroupID),
					zap.Int64("leaf_group_id", preferred),
				)
			}
		}
	}
	if len(compatible) > 0 {
		leafIDs, firstKey = append(compatible, demoted...), compatibleFirstKey
	} else {
		leafIDs, firstKey = demoted, demotedFirstKey
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
		LeafSessionKey: h.adaptiveLeafSessionKey(c, apiKey),
	}
	if billing, berr := h.authorizeGatewayAdaptive(ctx, apiKey, plan, requestedModel, body, reqLog); berr != nil {
		if reqLog != nil {
			reqLog.Warn("gateway.adaptive_authorize_failed",
				zap.Int64("parent_group_id", *apiKey.GroupID),
				zap.Error(berr),
			)
		}
		return apiKey
	} else {
		session.Billing = billing
		if billing != nil && len(leafIDs) > 0 {
			h.freezeGatewayAdaptiveLeafUserMultiplier(ctx, session, leafIDs[0])
		}
	}
	if _, err := h.adaptiveBilling.MarkInFlight(ctx, session.Billing, leafIDs[0], 1); err != nil {
		h.releaseGatewayAdaptive(ctx, session, "mark_in_flight_failed", reqLog)
		bindAdaptiveExecutionScope(c, apiKey.Group, nil)
		return apiKey
	}
	bindAdaptivePlanLeaf(c, plan, firstKey.Group)
	// Remember the leaf this session landed on so the next request starts from
	// it instead of re-deriving an order that may point at another pool.
	h.rememberAdaptiveLeafIfNeeded(ctx, session, leafIDs[0])
	if c != nil {
		c.Set(ginKeyGatewayAdaptiveSession, session)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), gatewayAdaptiveSessionContextKey{}, session))
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
	bindAdaptiveExecutionScope(c, nil, nil)
	if h.adaptiveBilling == nil || session.Billing == nil {
		return nil
	}
	if _, err := h.adaptiveBilling.MarkAttemptFailed(ctx, session.Billing, "precommit_upstream"); err != nil {
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
	h.freezeGatewayAdaptiveLeafUserMultiplier(ctx, session, gid)
	if _, err := h.adaptiveBilling.MarkInFlight(ctx, session.Billing, gid, next+1); err != nil {
		return nil
	}
	bindAdaptivePlanLeaf(c, session.Plan, group)
	// The session moved pools, so the remembered leaf must move with it —
	// otherwise the next request would try to return to the pool that just
	// failed and lose the cache again.
	h.rememberAdaptiveLeafIfNeeded(ctx, session, gid)
	return cloneAPIKeyWithGroup(rootKey, group)
}

// rememberAdaptiveLeafIfNeeded persists the leaf the session bound to. Writes
// once per request: the first successful bind is the meaningful one, and later
// failover hops only refresh it when the leaf actually changed.
func (h *GatewayHandler) rememberAdaptiveLeafIfNeeded(ctx context.Context, session *gatewayAdaptiveSession, leafGroupID int64) {
	if session == nil || leafGroupID <= 0 || h == nil || h.gatewayService == nil {
		return
	}
	if session.LeafSessionKey == "" {
		return
	}
	if session.StickyLeafFresh {
		return
	}
	h.gatewayService.RememberAdaptiveLeaf(ctx, session.ParentGroupID, session.LeafSessionKey, leafGroupID)
	session.StickyLeafFresh = true
}

func (h *GatewayHandler) releaseGatewayAdaptive(ctx context.Context, session *gatewayAdaptiveSession, reason string, reqLog *zap.Logger) {
	if session == nil || session.Billing == nil || h.adaptiveBilling == nil {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := h.adaptiveBilling.Release(cleanup, session.Billing, reason, ""); err != nil && reqLog != nil {
		reqLog.Warn("gateway.adaptive_release_failed", zap.Error(err))
	}
}

func (h *GatewayHandler) finishGatewayAdaptiveRequest(ctx context.Context, c *gin.Context, reqLog *zap.Logger) {
	session := getGatewayAdaptiveSession(c)
	// Once usage is queued, capture owns the hold. Releasing at HTTP return
	// would race the mandatory asynchronous settlement worker.
	if session != nil && !session.UsageSubmitted {
		h.releaseGatewayAdaptive(ctx, session, "request_end_without_usage", reqLog)
	}
}

func gatewayAdaptiveHoldActive(c *gin.Context) bool {
	session := getGatewayAdaptiveSession(c)
	return session != nil && session.Billing != nil && !session.Billing.Probe
}

func gatewayAdaptiveBilling(c *gin.Context) *service.AdaptiveBillingContext {
	session := getGatewayAdaptiveSession(c)
	if session == nil {
		return nil
	}
	return session.Billing
}

func (h *GatewayHandler) authorizeGatewayAdaptive(
	ctx context.Context,
	apiKey *service.APIKey,
	plan *service.AdaptiveRoutePlan,
	requestedModel string,
	body []byte,
	reqLog *zap.Logger,
) (*service.AdaptiveBillingContext, error) {
	if h == nil || h.adaptiveBilling == nil || apiKey == nil || apiKey.User == nil || plan == nil {
		return nil, nil
	}
	var resolveUserRate func(context.Context, int64, int64, float64) float64
	if h.gatewayService != nil {
		resolveUserRate = h.gatewayService.ResolveUserGroupRateMultiplier
	}
	maxRate := adaptiveHoldRate(ctx, plan.Candidates(), apiKey.User.ID, resolveUserRate)
	estimatedBase := decimal.NewFromFloat(adaptiveV1HoldBaseUSD * maxRate).Round(service.AdaptiveBillingMoneyScale)
	if estimatedBase.IsNegative() || estimatedBase.IsZero() {
		estimatedBase = decimal.NewFromFloat(adaptiveV1HoldBaseUSD)
	}
	if h.gatewayService != nil {
		model := plan.CanonicalModel
		if model == "" {
			model = requestedModel
		}
		pricingAt := service.GatewayTokenRequestPricingAtFromContext(ctx)
		if pricingAt.IsZero() {
			pricingAt = time.Now()
		}
		if priced, err := h.gatewayService.EstimateAdaptiveHoldBase(
			ctx, apiKey, body, model, pricingAt,
			gjson.GetBytes(body, "service_tier").String(), maxRate,
		); err != nil {
			if reqLog != nil {
				reqLog.Warn("gateway.adaptive_hold_estimate_fallback", zap.Error(err))
			}
		} else if priced.IsPositive() {
			estimatedBase = priced
		}
	}
	parentID := plan.ParentGroupID
	logicalID := uuid.NewString()
	ownerID, _ := os.Hostname()
	if ownerID == "" {
		ownerID = "sub2api"
	}
	billing, _, err := h.adaptiveBilling.Authorize(ctx, &service.UsageReservationReserveCommand{
		ReservationID:       uuid.NewString(),
		IdempotencyKey:      "adaptive:" + logicalID,
		LogicalRequestID:    logicalID,
		OwnerID:             ownerID,
		UserID:              apiKey.User.ID,
		APIKeyID:            apiKey.ID,
		ParentGroupID:       &parentID,
		CanonicalModel:      plan.CanonicalModel,
		PricingSnapshotID:   plan.PricingSnapshotID,
		PricingGeneration:   plan.PricingGeneration,
		ConfigGeneration:    plan.ConfigGeneration,
		FundingSource:       service.UsageReservationFundingBalance,
		EstimatedBaseCost:   estimatedBase,
		ManagementFeeBPS:    h.gatewayAdaptiveServiceFeeBPS(ctx),
		ManagementFeeBPSSet: true,
		LeaseTTL:            2 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	billing.UserID = apiKey.User.ID
	billing.ManagementFeeBPSSet = true
	if reqLog != nil {
		reqLog.Info("gateway.adaptive_authorized",
			zap.String("reservation_id", billing.ReservationID),
			zap.String("held_total", billing.HeldTotal.String()),
			zap.String("estimated_base", estimatedBase.String()),
		)
	}
	return billing, nil
}

func (h *GatewayHandler) freezeGatewayAdaptiveLeafUserMultiplier(ctx context.Context, session *gatewayAdaptiveSession, leafGroupID int64) {
	if h == nil || session == nil || session.Billing == nil || session.Plan == nil || leafGroupID <= 0 {
		return
	}
	leafRate := -1.0
	for _, candidate := range session.Plan.Candidates() {
		if candidate.LeafGroupID == leafGroupID {
			leafRate = candidate.FrozenRateMultiplier
			break
		}
	}
	if leafRate < 0 {
		return
	}
	effective := leafRate
	if h.gatewayService != nil && session.Billing.UserID > 0 {
		effective = h.gatewayService.ResolveUserGroupRateMultiplier(ctx, session.Billing.UserID, leafGroupID, leafRate)
	}
	if effective < 0 {
		return
	}
	session.Billing.UserRateMultiplier = effective
	session.Billing.UserRateMultiplierSet = true
}

func (h *GatewayHandler) gatewayAdaptiveServiceFeeBPS(ctx context.Context) int32 {
	if h == nil || h.settingService == nil {
		return service.DefaultAdaptiveManagementFeeBPS
	}
	return h.settingService.GetAdaptiveServiceFeeBPS(ctx)
}
