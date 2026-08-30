package handler

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

const (
	ginKeyOpenAIAdaptiveSession          = "openai_adaptive_session"
	ginKeyOpenAIAdaptiveAntiStallCleanup = "openai_adaptive_anti_stall_cleanup"

	// adaptiveV1HoldBaseUSD is a conservative leaf-equivalent charge envelope used
	// before real token usage is known. HOLD = ceil(base * maxLeafRate * 1.15).
	// Tunable later; must stay high enough to cover normal GPT responses without
	// frequent mid-flight renew.
	adaptiveV1HoldBaseUSD           = 5.0
	adaptivePassiveFailureThreshold = 3
)

// openAIAdaptiveSession is request-scoped state for one Adaptive parent-group call.
type openAIAdaptiveSession struct {
	ParentGroupID               int64
	Plan                        *service.AdaptiveRoutePlan
	Billing                     *service.AdaptiveBillingContext
	Attempts                    []openAIFallbackGroupAttempt
	Captured                    bool
	CurrentLeafID               int64
	CurrentLeafUpstreamFailures int
	LeafStartedAt               time.Time
	CanonicalModel              string

	billingMu         sync.Mutex
	heartbeatCancel   context.CancelFunc
	heartbeatDone     <-chan struct{}
	heartbeatStopOnce sync.Once
	SettlementQueued  bool
	billingFinalizing bool
}

// buildOpenAIGroupAttempts selects either Adaptive leaf attempts (when the
// key's primary group is an Adaptive parent and routing is enabled) or the
// legacy fallback_group_ids chain. Adaptive and key-level fallback are never
// combined, so attempt budgets stay bounded.
func (h *OpenAIGatewayHandler) buildOpenAIGroupAttempts(
	c *gin.Context,
	apiKey *service.APIKey,
	requestedModel string,
	protocol service.AdaptiveRouteProtocol,
	allowGrok bool,
	resolveGroups bool,
	reqLog *zap.Logger,
) []openAIFallbackGroupAttempt {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if session, ok := h.startAdaptiveOpenAISession(ctx, c, apiKey, requestedModel, protocol, allowGrok, reqLog); ok {
		if c != nil {
			c.Set(ginKeyOpenAIAdaptiveSession, session)
		}
		return session.Attempts
	}
	return h.buildOpenAIFallbackGroupAttempts(ctx, apiKey, allowGrok, resolveGroups, reqLog)
}

func getOpenAIAdaptiveSession(c *gin.Context) *openAIAdaptiveSession {
	if c == nil {
		return nil
	}
	v, ok := c.Get(ginKeyOpenAIAdaptiveSession)
	if !ok || v == nil {
		return nil
	}
	session, _ := v.(*openAIAdaptiveSession)
	return session
}

// startAdaptiveOpenAISession returns true when Adaptive routing owns this key.
// On ownership it plans leaves, authorizes HOLD (base * 1.15), and builds attempts.
func (h *OpenAIGatewayHandler) startAdaptiveOpenAISession(
	ctx context.Context,
	c *gin.Context,
	apiKey *service.APIKey,
	requestedModel string,
	protocol service.AdaptiveRouteProtocol,
	allowGrok bool,
	reqLog *zap.Logger,
) (*openAIAdaptiveSession, bool) {
	if h == nil || apiKey == nil || apiKey.GroupID == nil {
		return nil, false
	}
	if h.cfg == nil || !h.cfg.Gateway.AdaptiveRoutingEnabled {
		return nil, false
	}
	if h.adaptivePlanner == nil || h.adaptiveBilling == nil {
		return nil, false
	}

	parentID := *apiKey.GroupID
	platform := service.PlatformOpenAI
	if apiKey.Group != nil && apiKey.Group.Platform != "" {
		platform = apiKey.Group.Platform
	}
	if platform == service.PlatformGrok && !allowGrok {
		return nil, false
	}

	routeMode := service.AdaptiveRouteModeFromPreference(apiKey.AdaptiveRoutingPreference)
	// Prefer pool flag when planner loads snapshot; pass true only if we can
	// cheaply know it — full flag is applied inside Plan from snapshot.
	plan, err := h.adaptivePlanner.Plan(ctx, service.AdaptiveRouteRequest{
		ParentGroupID:     parentID,
		Platform:          platform,
		RequestedModel:    requestedModel,
		Mode:              routeMode,
		MaxRateMultiplier: apiKey.AdaptiveMaxRateMultiplier,
		Protocol:          protocol,
	})
	if err != nil {
		if errors.Is(err, service.ErrAdaptivePoolNotFound) || errors.Is(err, service.ErrAdaptivePoolDisabled) {
			return nil, false
		}
		if reqLog != nil {
			reqLog.Warn("openai.adaptive_plan_failed",
				zap.Int64("parent_group_id", parentID),
				zap.String("model", requestedModel),
				zap.Error(err),
			)
		}
		return &openAIAdaptiveSession{ParentGroupID: parentID, Attempts: nil}, true
	}
	if plan == nil || plan.CandidateCount() == 0 {
		return &openAIAdaptiveSession{ParentGroupID: parentID, Plan: plan, Attempts: nil}, true
	}

	candidates := plan.Candidates()
	attempts := make([]openAIFallbackGroupAttempt, 0, len(candidates))
	for i, candidate := range candidates {
		group, gerr := h.resolveAdaptiveLeafGroup(ctx, candidate.LeafGroupID, reqLog)
		if gerr != nil || group == nil {
			continue
		}
		leafKey := cloneAPIKeyWithGroup(apiKey, group)
		gid := group.ID
		attempts = append(attempts, openAIFallbackGroupAttempt{
			APIKey:   leafKey,
			Group:    group,
			GroupID:  &gid,
			Index:    i,
			Fallback: i > 0,
		})
	}
	if len(attempts) == 0 {
		return &openAIAdaptiveSession{ParentGroupID: parentID, Plan: plan, Attempts: nil}, true
	}

	billing, authErr := h.authorizeAdaptiveOpenAI(ctx, apiKey, plan, reqLog)
	if authErr != nil {
		if reqLog != nil {
			reqLog.Warn("openai.adaptive_authorize_failed",
				zap.Int64("parent_group_id", parentID),
				zap.Error(authErr),
			)
		}
		// Owned as Adaptive but cannot hold funds: fail closed (empty attempts).
		return &openAIAdaptiveSession{ParentGroupID: parentID, Plan: plan, Attempts: nil}, true
	}

	session := &openAIAdaptiveSession{
		ParentGroupID:  parentID,
		Plan:           plan,
		Billing:        billing,
		Attempts:       attempts,
		CanonicalModel: plan.CanonicalModel,
	}
	session.startAdaptiveBillingHeartbeat(
		h.adaptiveBilling,
		service.UsageReservationRenewInterval,
		service.UsageReservationDefaultLease,
		reqLog,
	)
	// Anti-Stall PRO: per-key tier opt-in; admin params per basic/pro/ultra.
	if h.settingService != nil && c != nil && apiKey != nil {
		adminCfg := h.settingService.GetAntiStallAdminConfig(ctx)
		antiCfg := service.ResolveAntiStallForKey(adminCfg, apiKey.AntiStallTier)
		if antiCfg.Enabled {
			antiSession := service.NewAntiStallSession(antiCfg)
			if installAdaptiveAntiStallWriterOnce(c, antiSession) && reqLog != nil {
				reqLog.Info("openai.anti_stall_pro_enabled",
					zap.String("tier", antiCfg.Tier),
					zap.Int("buffer_tokens", antiCfg.BufferTokens),
					zap.Int("drip_per_sec", antiCfg.DripTokensPerSecond),
					zap.Int("upstream_max_retry", antiCfg.UpstreamMaxRetry),
					zap.Int("max_drip_seconds", antiCfg.MaxDripSeconds),
					zap.Int("max_leaf_switches", antiCfg.MaxLeafSwitches),
				)
			}
		}
	}
	if reqLog != nil {
		leafIDs := make([]int64, 0, len(attempts))
		for _, a := range attempts {
			if a.GroupID != nil {
				leafIDs = append(leafIDs, *a.GroupID)
			}
		}
		reqLog.Info("openai.adaptive_plan_ready",
			zap.Int64("parent_group_id", parentID),
			zap.String("model", plan.CanonicalModel),
			zap.String("route_mode", string(plan.Mode)),
			zap.String("key_preference", apiKey.AdaptiveRoutingPreference),
			zap.Any("max_rate_budget", apiKey.AdaptiveMaxRateMultiplier),
			zap.String("anti_stall_tier", apiKey.AntiStallTier),
			zap.Int("leaf_count", len(attempts)),
			zap.Int64s("leaf_order", leafIDs),
			zap.String("pricing_snapshot_id", plan.PricingSnapshotID),
			zap.String("reservation_id", billing.ReservationID),
		)
	}
	return session, true
}

func installAdaptiveAntiStallWriterOnce(c *gin.Context, session *service.AntiStallSession) bool {
	if c == nil || session == nil || service.AntiStallSessionFromGin(c) != nil {
		return false
	}
	cleanup := service.InstallAntiStallProWriter(c, session)
	c.Set(ginKeyOpenAIAdaptiveAntiStallCleanup, cleanup)
	return true
}

func finishAdaptiveAntiStallWriter(c *gin.Context) {
	if c == nil {
		return
	}
	value, ok := c.Get(ginKeyOpenAIAdaptiveAntiStallCleanup)
	if !ok || value == nil {
		return
	}
	c.Set(ginKeyOpenAIAdaptiveAntiStallCleanup, nil)
	if cleanup, ok := value.(func()); ok && cleanup != nil {
		cleanup()
	}
}

func (h *OpenAIGatewayHandler) authorizeAdaptiveOpenAI(
	ctx context.Context,
	apiKey *service.APIKey,
	plan *service.AdaptiveRoutePlan,
	reqLog *zap.Logger,
) (*service.AdaptiveBillingContext, error) {
	if h == nil || h.adaptiveBilling == nil || apiKey == nil || plan == nil {
		return nil, service.ErrAdaptiveBillingContextInvalid
	}
	if apiKey.User == nil {
		return nil, service.ErrAdaptiveBillingContextInvalid
	}

	maxRate := 0.0
	for _, candidate := range plan.Candidates() {
		if candidate.FrozenRateMultiplier > maxRate {
			maxRate = candidate.FrozenRateMultiplier
		}
		if candidate.MaximumAccountRateMultiplier > maxRate {
			maxRate = candidate.MaximumAccountRateMultiplier
		}
	}
	if maxRate <= 0 || math.IsNaN(maxRate) || math.IsInf(maxRate, 0) {
		maxRate = 1.0
	}
	estimatedBase := decimal.NewFromFloat(adaptiveV1HoldBaseUSD * maxRate).Round(service.AdaptiveBillingMoneyScale)
	if estimatedBase.IsNegative() || estimatedBase.IsZero() {
		estimatedBase = decimal.NewFromFloat(adaptiveV1HoldBaseUSD)
	}

	parentID := plan.ParentGroupID
	funding := service.UsageReservationFundingBalance
	// Adaptive parent groups are balance-funded for HOLD. Leaf group rate
	// multipliers still determine base charge B on capture.

	logicalID := uuid.NewString()
	ownerID, _ := os.Hostname()
	if ownerID == "" {
		ownerID = "sub2api"
	}
	reservationID := uuid.NewString()

	// Leave RequestFingerprint empty so Normalize() derives a SHA-256 over the
	// reserve identity. A raw UUID is not a valid fingerprint and was failing
	// Authorize with ErrUsageReservationInvalid → client 502.
	billing, _, err := h.adaptiveBilling.Authorize(ctx, &service.UsageReservationReserveCommand{
		ReservationID:     reservationID,
		IdempotencyKey:    "adaptive:" + logicalID,
		LogicalRequestID:  logicalID,
		OwnerID:           ownerID,
		UserID:            apiKey.User.ID,
		APIKeyID:          apiKey.ID,
		ParentGroupID:     &parentID,
		CanonicalModel:    plan.CanonicalModel,
		PricingSnapshotID: plan.PricingSnapshotID,
		PricingGeneration: plan.PricingGeneration,
		ConfigGeneration:  plan.ConfigGeneration,
		FundingSource:     funding,
		EstimatedBaseCost: estimatedBase,
		ManagementFeeBPS:  service.DefaultAdaptiveManagementFeeBPS,
		LeaseTTL:          2 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	if reqLog != nil {
		reqLog.Info("openai.adaptive_authorized",
			zap.String("reservation_id", billing.ReservationID),
			zap.String("held_total", billing.HeldTotal.String()),
			zap.String("estimated_base", estimatedBase.String()),
		)
	}
	return billing, nil
}

// markAdaptiveLeafInFlight records the leaf attempt before upstream work.
// It returns false when the in-flight fence could not be persisted, allowing
// callers to fail closed before sending any upstream request.
func (h *OpenAIGatewayHandler) markAdaptiveLeafInFlight(ctx context.Context, c *gin.Context, leafGroupID int64, attemptNo int, reqLog *zap.Logger) bool {
	if h == nil {
		return false
	}
	session := getOpenAIAdaptiveSession(c)
	if session == nil {
		// Non-adaptive request (e.g. a key bound directly to a leaf group):
		// there is no in-flight fence to persist, so never fail closed here.
		return true
	}
	if leafGroupID <= 0 || attemptNo < 1 {
		return false
	}
	session.billingMu.Lock()
	defer session.billingMu.Unlock()
	session.CurrentLeafID = leafGroupID
	session.CurrentLeafUpstreamFailures = 0
	session.LeafStartedAt = time.Now()
	if session.Billing == nil || h.adaptiveBilling == nil {
		return false
	}
	if _, err := h.adaptiveBilling.MarkInFlight(ctx, session.Billing, leafGroupID, attemptNo); err != nil {
		if reqLog != nil {
			reqLog.Warn("openai.adaptive_mark_in_flight_failed",
				zap.Int64("leaf_group_id", leafGroupID),
				zap.Int("attempt_no", attemptNo),
				zap.Error(err),
			)
		}
		return false
	}
	return true
}

// markAdaptiveLeafFailed records a non-committed leaf failure before switching.
func (h *OpenAIGatewayHandler) markAdaptiveLeafFailed(ctx context.Context, c *gin.Context, failureClass string, reqLog *zap.Logger) {
	session := getOpenAIAdaptiveSession(c)
	if session == nil || h == nil {
		return
	}
	session.billingMu.Lock()
	defer session.billingMu.Unlock()
	h.recordAdaptiveLeafSignal(session, false, 0, 0)
	if session.Billing == nil || h.adaptiveBilling == nil {
		return
	}
	precommitClass := normalizeAdaptivePrecommitFailureClass(failureClass)
	if _, err := h.adaptiveBilling.MarkAttemptFailed(ctx, session.Billing, precommitClass); err != nil {
		if reqLog != nil {
			reqLog.Warn("openai.adaptive_mark_attempt_failed",
				zap.String("failure_class", precommitClass),
				zap.String("failure_reason", failureClass),
				zap.Error(err),
			)
		}
	}
	// Anti-Stall PRO: enter drip/recovery; leaf switch is decided by caller.
	// Background drip keeps the client from freezing while we retry/switch.
	// Offer() on reconnect exits drip so the user is not stuck at 1 token/s.
	if anti := service.AntiStallSessionFromGin(c); anti != nil {
		anti.BeginRecovery()
		service.EnsureAntiStallDripRunning(c, anti)
	}
}

func normalizeAdaptivePrecommitFailureClass(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "precommit_transport", "precommit_upstream", "precommit_policy", "precommit_cancelled":
		return normalized
	}
	switch {
	case strings.Contains(normalized, "cancel"), strings.Contains(normalized, "disconnect"):
		return "precommit_cancelled"
	case strings.Contains(normalized, "upstream"):
		return "precommit_upstream"
	case strings.Contains(normalized, "transport"), strings.Contains(normalized, "network"),
		strings.Contains(normalized, "timeout"), strings.Contains(normalized, "dial"):
		return "precommit_transport"
	default:
		return "precommit_policy"
	}
}

// recordAdaptiveLeafSignalSuccess records a successful leaf outcome for passive routing.
func (h *OpenAIGatewayHandler) recordAdaptiveLeafSignalSuccess(c *gin.Context, firstTokenMs, totalMs float64) {
	session := getOpenAIAdaptiveSession(c)
	if session == nil {
		return
	}
	h.recordAdaptiveLeafSignal(session, true, firstTokenMs, totalMs)
}

func (h *OpenAIGatewayHandler) recordAdaptiveLeafSignal(session *openAIAdaptiveSession, success bool, firstTokenMs, totalMs float64) {
	if h == nil || h.adaptivePlanner == nil || session == nil {
		return
	}
	leafID := session.CurrentLeafID
	if leafID <= 0 && session.Billing != nil && session.Billing.LeafGroupID > 0 {
		leafID = session.Billing.LeafGroupID
	}
	if leafID <= 0 || session.ParentGroupID <= 0 {
		return
	}
	model := session.CanonicalModel
	if model == "" && session.Plan != nil {
		model = session.Plan.CanonicalModel
	}
	if model == "" {
		return
	}
	if totalMs <= 0 && !session.LeafStartedAt.IsZero() {
		totalMs = float64(time.Since(session.LeafStartedAt).Milliseconds())
	}
	h.adaptivePlanner.RecordLeafOutcome(service.AdaptiveLeafOutcome{
		ParentGroupID:       session.ParentGroupID,
		LeafGroupID:         leafID,
		CanonicalModel:      model,
		Success:             success,
		FirstTokenLatencyMS: firstTokenMs,
		TotalLatencyMS:      totalMs,
		ObservedAt:          time.Now(),
	})
}

// finishAdaptiveOpenAISession releases an unused hold when nothing was captured.
func (h *OpenAIGatewayHandler) finishAdaptiveOpenAISession(ctx context.Context, c *gin.Context, reason string, reqLog *zap.Logger) error {
	session := getOpenAIAdaptiveSession(c)
	if session == nil || !session.beginAdaptiveBillingRelease() {
		return nil
	}
	session.stopAdaptiveBillingHeartbeat()
	releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session.billingMu.Lock()
	defer session.billingMu.Unlock()
	if session.Billing != nil && session.Billing.AttemptNo > 0 && h != nil && h.adaptiveBilling != nil {
		h.recordAdaptiveLeafSignal(session, false, 0, 0)
		failureClass := normalizeAdaptivePrecommitFailureClass(reason)
		if _, err := h.adaptiveBilling.MarkAttemptFailed(releaseCtx, session.Billing, failureClass); err != nil {
			if reqLog != nil {
				reqLog.Warn("openai.adaptive_finalize_attempt_failed",
					zap.String("reason", reason),
					zap.String("failure_class", failureClass),
					zap.Error(err),
				)
			}
			return err
		}
	}
	if err := service.ReleaseAdaptiveCustomerHold(releaseCtx, h.adaptiveBilling, session.Billing, reason); err != nil {
		if reqLog != nil {
			reqLog.Warn("openai.adaptive_release_failed",
				zap.String("reason", reason),
				zap.Error(err),
			)
		}
		return err
	}
	return nil
}

func (h *OpenAIGatewayHandler) finishAdaptiveOpenAIRequest(ctx context.Context, c *gin.Context, reason string, reqLog *zap.Logger) (err error) {
	defer finishAdaptiveAntiStallWriter(c)
	return h.finishAdaptiveOpenAISession(ctx, c, reason, reqLog)
}

func (h *OpenAIGatewayHandler) resetAdaptiveOpenAISession(ctx context.Context, c *gin.Context, reason string, reqLog *zap.Logger) error {
	if getOpenAIAdaptiveSession(c) == nil {
		return nil
	}
	if err := h.finishAdaptiveOpenAISession(ctx, c, reason, reqLog); err != nil {
		return err
	}
	c.Set(ginKeyOpenAIAdaptiveSession, nil)
	return nil
}

func prepareAdaptiveSessionSettlement(c *gin.Context) (*service.AdaptiveBillingContext, *openAIAdaptiveSession) {
	session := getOpenAIAdaptiveSession(c)
	if session == nil || !session.queueAdaptiveBillingSettlement() {
		return nil, nil
	}
	if anti := service.AntiStallSessionFromGin(c); anti != nil {
		anti.EndRecovery()
		service.StopAntiStallDrip(c)
	}
	// Settlement works on a deep snapshot: the heartbeat may still renew the
	// original context under billingMu, so concurrent RowVersion/FencingToken
	// writes can never race with Capture reads on the settlement copy.
	return service.CloneAdaptiveBillingContext(session.Billing), session
}

func (s *openAIAdaptiveSession) startAdaptiveBillingHeartbeat(
	coordinator *service.AdaptiveBillingCoordinator,
	interval time.Duration,
	leaseTTL time.Duration,
	reqLog *zap.Logger,
) {
	if s == nil || s.Billing == nil || coordinator == nil || interval <= 0 || leaseTTL <= 0 {
		return
	}
	heartbeatCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.billingMu.Lock()
	s.heartbeatCancel = cancel
	s.heartbeatDone = done
	s.billingMu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
			}

		s.billingMu.Lock()
		if s.billingFinalizing || s.Captured {
			s.billingMu.Unlock()
			return
		}
		// Settlement queued closes the version-conflict window: finish the
		// in-flight tick (an already scheduled renewal may still complete),
		// then stop issuing further renewals.
		stopAfterRenew := s.SettlementQueued
		renewCtx, renewCancel := context.WithTimeout(heartbeatCtx, 10*time.Second)
		_, err := coordinator.Renew(renewCtx, s.Billing, decimal.Zero, leaseTTL)
		renewCancel()
		s.billingMu.Unlock()
		if stopAfterRenew {
			return
		}
			if err != nil {
				if reqLog != nil {
					reqLog.Warn("openai.adaptive_renew_failed",
						zap.String("reservation_id", s.Billing.ReservationID),
						zap.Error(err),
					)
				}
				if errors.Is(err, service.ErrUsageReservationNotHeld) ||
					errors.Is(err, service.ErrUsageReservationFenceConflict) ||
					errors.Is(err, service.ErrUsageReservationOwnerConflict) ||
					errors.Is(err, service.ErrUsageReservationLeaseExpired) {
					return
				}
			}
		}
	}()
}

func (s *openAIAdaptiveSession) stopAdaptiveBillingHeartbeat() {
	if s == nil {
		return
	}
	s.heartbeatStopOnce.Do(func() {
		s.billingMu.Lock()
		cancel := s.heartbeatCancel
		done := s.heartbeatDone
		s.billingMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	})
}

func (s *openAIAdaptiveSession) queueAdaptiveBillingSettlement() bool {
	if s == nil {
		return false
	}
	s.billingMu.Lock()
	defer s.billingMu.Unlock()
	if s.Billing == nil || s.Captured || s.SettlementQueued || s.billingFinalizing {
		return false
	}
	s.SettlementQueued = true
	return true
}

func (s *openAIAdaptiveSession) beginAdaptiveBillingRelease() bool {
	if s == nil {
		return false
	}
	s.billingMu.Lock()
	defer s.billingMu.Unlock()
	if s.Billing == nil || s.Captured || s.SettlementQueued || s.billingFinalizing {
		return false
	}
	s.billingFinalizing = true
	return true
}

func (s *openAIAdaptiveSession) markAdaptiveBillingCaptured() {
	if s == nil {
		return
	}
	s.billingMu.Lock()
	s.Captured = true
	s.billingMu.Unlock()
}

func adaptiveTimingsFromOpenAIResult(result *service.OpenAIForwardResult) (ttftMS, totalMS float64) {
	if result == nil {
		return 0, 0
	}
	if result.Duration > 0 {
		totalMS = float64(result.Duration.Milliseconds())
	}
	if ptr := result.FirstTokenMsForScheduling(); ptr != nil && *ptr > 0 {
		ttftMS = float64(*ptr)
	} else if result.FirstTokenMs != nil && *result.FirstTokenMs > 0 {
		ttftMS = float64(*result.FirstTokenMs)
	}
	return ttftMS, totalMS
}

func (h *OpenAIGatewayHandler) resolveAdaptiveLeafGroup(ctx context.Context, leafGroupID int64, reqLog *zap.Logger) (*service.Group, error) {
	if leafGroupID <= 0 || h == nil || h.apiKeyService == nil {
		return nil, service.ErrAdaptiveRouteUnavailable
	}
	group, err := h.apiKeyService.ResolveGroupByID(ctx, leafGroupID)
	if err != nil {
		if reqLog != nil {
			reqLog.Warn("openai.adaptive_resolve_leaf_failed",
				zap.Int64("leaf_group_id", leafGroupID),
				zap.Error(err),
			)
		}
		return nil, err
	}
	return group, nil
}

// SetAdaptiveRouting attaches planner + billing coordinator used by Adaptive
// parent groups. Optional for tests that only exercise physical groups.
func (h *OpenAIGatewayHandler) SetAdaptiveRouting(planner *service.AdaptiveRoutePlanner, billing *service.AdaptiveBillingCoordinator) {
	if h == nil {
		return
	}
	h.adaptivePlanner = planner
	h.adaptiveBilling = billing
}

// SetSettingService wires Anti-Stall PRO runtime settings.
func (h *OpenAIGatewayHandler) SetSettingService(settings *service.SettingService) {
	if h != nil {
		h.settingService = settings
	}
}
