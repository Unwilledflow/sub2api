package handler

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	competitiveAnthropicFanout             = 2
	competitiveAnthropicProbeBudget        = competitiveAnthropicFanout * 3
	competitiveAnthropicAttemptSequenceKey = "competitive_anthropic_attempt_sequence"
)

type competitiveAnthropicSelectFunc func(context.Context, map[int64]struct{}) (*service.AccountSelectionResult, error)

type competitiveAnthropicForwardFunc func(context.Context, *gin.Context, *service.Account) (*service.ForwardResult, *service.ParsedRequest, error)

type competitiveAnthropicCandidate struct {
	account   *service.Account
	release   func()
	attemptNo int
}

func competitiveAnthropicRouteKey(account *service.Account) string {
	if account == nil {
		return ""
	}

	endpoint := ""
	if account.IsCustomBaseURLEnabled() {
		endpoint = strings.TrimSpace(account.GetCustomBaseURL())
	}
	if endpoint == "" {
		endpoint = strings.TrimSpace(account.GetBaseURL())
	}
	if endpoint == "" {
		endpoint = "default:" + strings.ToLower(strings.TrimSpace(account.Platform))
	} else if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
		endpoint = parsed.String()
	} else {
		endpoint = strings.ToLower(strings.TrimRight(endpoint, "/"))
	}

	proxy := "direct"
	if account.ProxyID != nil {
		proxy = "proxy-id:" + strconv.FormatInt(*account.ProxyID, 10)
	} else if account.Proxy != nil {
		if account.Proxy.ID != 0 {
			proxy = "proxy-id:" + strconv.FormatInt(account.Proxy.ID, 10)
		} else {
			proxy = "proxy-endpoint:" + strings.ToLower(strings.TrimSpace(account.Proxy.Protocol)) + "://" +
				strings.ToLower(strings.TrimSpace(account.Proxy.Host)) + ":" + strconv.Itoa(account.Proxy.Port)
		}
	}

	return endpoint + "|" + proxy
}

type competitiveAnthropicOutcome struct {
	id       int
	account  *service.Account
	ctx      *gin.Context
	result   *service.ForwardResult
	parsed   *service.ParsedRequest
	err      error
	duration time.Duration
}

func competitiveAnthropicEnabled(rootAPIKey, currentAPIKey *service.APIKey) bool {
	return groupCompetitiveConcurrencyEnabled(rootAPIKey) || groupCompetitiveConcurrencyEnabled(currentAPIKey)
}

func nextCompetitiveAnthropicAttemptNo(c *gin.Context) int {
	if c == nil {
		return 1
	}
	current, _ := c.Get(competitiveAnthropicAttemptSequenceKey)
	sequence, _ := current.(int)
	sequence++
	c.Set(competitiveAnthropicAttemptSequenceKey, sequence)
	return sequence
}

func (h *GatewayHandler) forwardCompetitiveAnthropicCandidate(
	ctx context.Context,
	attemptGin *gin.Context,
	account *service.Account,
	baseParsed *service.ParsedRequest,
	body []byte,
	channelMapping service.ChannelMappingResult,
	groupID *int64,
	hasBoundSession bool,
	forceCacheBilling bool,
	reqLog *zap.Logger,
) (*service.ForwardResult, *service.ParsedRequest, error) {
	attemptParsed, err := baseParsed.CloneForBody(body)
	if err != nil {
		return nil, nil, err
	}
	attemptParsed.GroupID = groupID
	if channelMapping.Mapped {
		attemptParsed.Model = channelMapping.MappedModel
		if err := attemptParsed.ReplaceBody(h.gatewayService.ReplaceModelInBody(attemptParsed.Body.Bytes(), channelMapping.MappedModel)); err != nil {
			return nil, attemptParsed, err
		}
	}
	if err := attemptParsed.ReplaceBody(h.gatewayService.ApplyBedrockCCCompat(attemptGin, attemptParsed.Body.Bytes(), attemptParsed.Model, account, groupID)); err != nil {
		return nil, attemptParsed, err
	}

	if reqLog == nil {
		reqLog = zap.NewNop()
	}
	attemptLog := reqLog.With(zap.Int64("competitive_account_id", account.ID))
	streamStarted := false
	var queueRelease func()
	switch mode := h.getUserMsgQueueMode(account, attemptParsed); mode {
	case config.UMQModeSerialize:
		release, queueErr := h.userMsgQueueHelper.AcquireWithWait(
			attemptGin,
			account.ID,
			account.GetBaseRPM(),
			attemptParsed.Stream,
			&streamStarted,
			h.cfg.Gateway.UserMessageQueue.WaitTimeout(),
			attemptLog,
		)
		if queueErr != nil {
			attemptLog.Warn("gateway.competitive_umq_acquire_failed", zap.Error(queueErr))
		} else {
			queueRelease = release
		}
	case config.UMQModeThrottle:
		if queueErr := h.userMsgQueueHelper.ThrottleWithPing(
			attemptGin,
			account.ID,
			account.GetBaseRPM(),
			attemptParsed.Stream,
			&streamStarted,
			h.cfg.Gateway.UserMessageQueue.WaitTimeout(),
			attemptLog,
		); queueErr != nil {
			attemptLog.Warn("gateway.competitive_umq_throttle_failed", zap.Error(queueErr))
		}
	default:
		if mode != "" {
			attemptLog.Warn("gateway.competitive_umq_unknown_mode", zap.String("mode", mode))
		}
	}

	queueRelease = wrapReleaseOnDone(ctx, queueRelease)
	attemptParsed.OnUpstreamAccepted = queueRelease
	defer func() {
		if queueRelease != nil {
			queueRelease()
		}
		attemptParsed.OnUpstreamAccepted = nil
	}()

	attemptGin.Set("parsed_request", attemptParsed)
	requestCtx := ctx
	if forceCacheBilling {
		requestCtx = service.WithForceCacheBilling(requestCtx)
	}
	if account.Platform == service.PlatformAntigravity && account.Type != service.AccountTypeAPIKey {
		result, err := h.antigravityGatewayService.Forward(requestCtx, attemptGin, account, attemptParsed.Body.Bytes(), hasBoundSession)
		return result, attemptParsed, err
	}
	result, err := h.gatewayService.Forward(requestCtx, attemptGin, account, attemptParsed)
	return result, attemptParsed, err
}

func (h *GatewayHandler) runCompetitiveAnthropicForward(
	c *gin.Context,
	firstOutputDeadline time.Time,
	groupID *int64,
	sessionHash string,
	initialSelection *service.AccountSelectionResult,
	initialRelease func(),
	excluded map[int64]struct{},
	model string,
	selectNext competitiveAnthropicSelectFunc,
	forward competitiveAnthropicForwardFunc,
	reqLog *zap.Logger,
) (*service.Account, *service.ForwardResult, *service.ParsedRequest, error) {
	if initialSelection == nil || initialSelection.Account == nil {
		if initialRelease != nil {
			initialRelease()
		}
		return nil, nil, nil, service.ErrNoAvailableAccounts
	}
	if firstOutputDeadline.IsZero() {
		firstOutputDeadline = time.Now().Add(competitiveConcurrencyFirstOutput)
	}
	requestStartedAt := firstOutputDeadline.Add(-competitiveConcurrencyFirstOutput)

	effectiveExcluded := cloneExcludedAccountIDsForCompetition(excluded)
	candidates := make([]competitiveAnthropicCandidate, 0, competitiveAnthropicFanout)
	selectedRouteKeys := make(map[string]struct{}, competitiveAnthropicFanout)
	deferredDuplicateRoutes := make([]competitiveAnthropicCandidate, 0, competitiveAnthropicFanout-1)
	coordinator := newCompetitiveResponseCoordinator(c.Writer)
	initialHeaders := c.Writer.Header().Clone()
	selectionCtx, cancelSelection := context.WithCancel(c.Request.Context())
	defer cancelSelection()
	coordinator.setSelectionCancel(cancelSelection)
	cancelers := make([]context.CancelFunc, 0, competitiveAnthropicFanout)
	resultCh := make(chan competitiveAnthropicOutcome, competitiveAnthropicFanout)
	startedAt := requestStartedAt
	startCandidate := func(candidate competitiveAnthropicCandidate) {
		if candidate.attemptNo <= 0 {
			candidate.attemptNo = nextCompetitiveAnthropicAttemptNo(c)
		}
		attemptID := len(candidates)
		candidates = append(candidates, candidate)
		attemptCtx, cancel := context.WithCancel(c.Request.Context())
		attemptCtx = service.WithCompetitiveUpstreamCancellation(attemptCtx)
		cancelers = append(cancelers, cancel)
		coordinator.setCancelers(cancelers)
		writer := newCompetitiveResponseWriter(attemptID, coordinator, initialHeaders)
		attemptGin := cloneGinContextForCompetition(c, attemptCtx, writer)
		go func(id int, candidate competitiveAnthropicCandidate, attemptGin *gin.Context, attemptCtx context.Context) {
			if candidate.release != nil {
				defer candidate.release()
			}
			attemptStart := time.Now()
			result, parsed, err := forward(attemptCtx, attemptGin, candidate.account)
			resultCh <- competitiveAnthropicOutcome{
				id:       id,
				account:  candidate.account,
				ctx:      attemptGin,
				result:   result,
				parsed:   parsed,
				err:      err,
				duration: time.Since(attemptStart),
			}
		}(attemptID, candidate, attemptGin, attemptCtx)
	}
	addSelection := func(selection *service.AccountSelectionResult, release func(), preferDistinctRoute bool) {
		if selection == nil || selection.Account == nil {
			if release != nil {
				release()
			}
			return
		}
		accountID := selection.Account.ID
		if _, duplicate := effectiveExcluded[accountID]; duplicate {
			if release != nil {
				release()
			}
			return
		}
		effectiveExcluded[accountID] = struct{}{}
		if !selection.Acquired {
			if release != nil {
				release()
			}
			return
		}
		candidate := competitiveAnthropicCandidate{
			account: selection.Account,
			release: wrapReleaseOnDone(c.Request.Context(), release),
		}
		routeKey := competitiveAnthropicRouteKey(selection.Account)
		if preferDistinctRoute {
			if _, duplicate := selectedRouteKeys[routeKey]; duplicate {
				deferredDuplicateRoutes = append(deferredDuplicateRoutes, candidate)
				return
			}
		}
		selectedRouteKeys[routeKey] = struct{}{}
		startCandidate(candidate)
	}
	releaseDeferredDuplicateRoutes := func() {
		for _, candidate := range deferredDuplicateRoutes {
			if candidate.release != nil {
				candidate.release()
			}
		}
		deferredDuplicateRoutes = deferredDuplicateRoutes[:0]
	}

	addSelection(initialSelection, initialRelease, false)
	if len(candidates) == 0 {
		return initialSelection.Account, nil, nil, service.ErrNoAvailableAccounts
	}

	hedgeStarted := false
	startHedges := func(reason string) {
		if hedgeStarted || coordinator.hasDecision() {
			return
		}
		hedgeStarted = true
		if reqLog != nil {
			reqLog.Info("anthropic.competitive_concurrency.hedge_started",
				zap.String("reason", reason),
				zap.Duration("threshold", competitiveConcurrencyFirstOutput),
				zap.Duration("elapsed", time.Since(requestStartedAt)),
			)
		}
		for probes := 0; len(candidates) < competitiveAnthropicFanout && selectNext != nil && probes < competitiveAnthropicProbeBudget; probes++ {
			selection, err := selectNext(selectionCtx, effectiveExcluded)
			if err != nil || selection == nil || selection.Account == nil {
				break
			}
			if coordinator.hasDecision() {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				releaseDeferredDuplicateRoutes()
				break
			}
			addSelection(selection, selection.ReleaseFunc, true)
		}
		if coordinator.hasDecision() {
			releaseDeferredDuplicateRoutes()
			return
		}
		for len(candidates) < competitiveAnthropicFanout && len(deferredDuplicateRoutes) > 0 {
			candidate := deferredDuplicateRoutes[0]
			deferredDuplicateRoutes = deferredDuplicateRoutes[1:]
			startCandidate(candidate)
		}
		releaseDeferredDuplicateRoutes()
	}

	remaining := time.Until(firstOutputDeadline)
	if remaining < 0 {
		remaining = 0
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	timerC := timer.C
	outcomes := make(map[int]competitiveAnthropicOutcome, competitiveAnthropicFanout)
	winnerID := -1
	for winnerID < 0 {
		if hedgeStarted && len(outcomes) == len(candidates) {
			break
		}
		select {
		case winnerID = <-coordinator.winnerCh:
		case outcome := <-resultCh:
			outcomes[outcome.id] = outcome
			if !coordinator.hasDecision() && !hedgeStarted {
				startHedges("initial_attempt_finished")
			}
		case <-timerC:
			timerC = nil
			startHedges("first_output_threshold")
		case <-c.Request.Context().Done():
			coordinator.cancelAll()
			return initialSelection.Account, nil, nil, c.Request.Context().Err()
		}
	}
	if winnerID < 0 {
		select {
		case winnerID = <-coordinator.winnerCh:
		default:
		}
	}
	if winnerID < 0 {
		coordinator.cancelAll()
		markCompetitiveAnthropicCandidatesExcluded(excluded, candidates)
		account, result, parsed, err := competitiveAnthropicFailureOutcome(candidates, outcomes)
		var failoverErr *service.UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			rememberPendingCompetitiveAnthropicWaste(c, model, candidates, outcomes)
		}
		logCompetitiveAnthropicSettlementEvidence(reqLog, -1, startedAt, candidates, outcomes, nil)
		return account, result, parsed, err
	}

	var winner competitiveAnthropicOutcome
	for {
		if outcome, ok := outcomes[winnerID]; ok {
			winner = outcome
			break
		}
		select {
		case outcome := <-resultCh:
			outcomes[outcome.id] = outcome
		case <-c.Request.Context().Done():
			coordinator.cancelAll()
			return candidates[winnerID].account, nil, nil, c.Request.Context().Err()
		}
	}

	cleanup := time.NewTimer(competitiveConcurrencyCleanupGrace)
	for len(outcomes) < len(candidates) {
		select {
		case outcome := <-resultCh:
			outcomes[outcome.id] = outcome
		case <-cleanup.C:
			goto cleanupComplete
		}
	}
	if !cleanup.Stop() {
		select {
		case <-cleanup.C:
		default:
		}
	}

cleanupComplete:
	mergeCompetitiveWinnerContext(c, winner.ctx)
	if winner.account != nil && h != nil && h.gatewayService != nil {
		winnerAt := coordinator.winnerDecisionAt()
		logicalTTFT := winnerAt.Sub(requestStartedAt)
		if slow, clearErr := h.clearSlowAnthropicStickySession(c, groupID, sessionHash, winner.account, logicalTTFT); slow {
			if clearErr != nil {
				if reqLog != nil {
					reqLog.Warn("anthropic.competitive_concurrency.slow_winner_sticky_clear_failed",
						zap.Int64("winner_account_id", winner.account.ID),
						zap.Duration("logical_ttft", logicalTTFT),
						zap.Error(clearErr),
					)
				}
			} else if reqLog != nil {
				reqLog.Info("anthropic.competitive_concurrency.slow_winner_sticky_cleared",
					zap.Int64("winner_account_id", winner.account.ID),
					zap.Duration("logical_ttft", logicalTTFT),
					zap.Duration("threshold", anthropicSlowStickyThreshold),
				)
			}
		} else if sessionHash != "" {
			// Align with ordinary path: do not overwrite an existing sticky binding
			// when the bound account was temporarily skipped and another account won.
			boundAccountID, bindErr := h.gatewayService.GetCachedSessionAccountID(c.Request.Context(), groupID, sessionHash)
			if bindErr != nil && !errors.Is(bindErr, service.ErrStickySessionNotFound) {
				// Redis read failure: skip write to avoid clobbering a healthy binding.
			} else if boundAccountID == 0 || boundAccountID == winner.account.ID {
				_ = h.gatewayService.BindStickySession(c.Request.Context(), groupID, sessionHash, winner.account.ID)
			}
		}
	}
	if winner.result != nil {
		mergePendingCompetitiveAnthropicWaste(c, winner.result)
		collectCompetitiveAnthropicWaste(
			winner.result,
			winnerID,
			model,
			candidates,
			outcomes,
			service.CompetitiveWasteReasonCanceledLoser,
		)
	}
	logCompetitiveAnthropicSettlementEvidence(reqLog, winnerID, startedAt, candidates, outcomes, winner.result)
	return winner.account, winner.result, winner.parsed, winner.err
}

func markCompetitiveAnthropicCandidatesExcluded(excluded map[int64]struct{}, candidates []competitiveAnthropicCandidate) {
	if excluded == nil {
		return
	}
	for _, candidate := range candidates {
		if candidate.account != nil {
			excluded[candidate.account.ID] = struct{}{}
		}
	}
}

func competitiveAnthropicFailureOutcome(candidates []competitiveAnthropicCandidate, outcomes map[int]competitiveAnthropicOutcome) (*service.Account, *service.ForwardResult, *service.ParsedRequest, error) {
	for id := range candidates {
		outcome, ok := outcomes[id]
		if !ok || outcome.err == nil {
			continue
		}
		var failoverErr *service.UpstreamFailoverError
		if errors.As(outcome.err, &failoverErr) {
			return outcome.account, outcome.result, outcome.parsed, outcome.err
		}
	}
	for id := range candidates {
		if outcome, ok := outcomes[id]; ok {
			if outcome.err != nil {
				return outcome.account, outcome.result, outcome.parsed, outcome.err
			}
			return outcome.account, outcome.result, outcome.parsed, errors.New("competitive upstream returned no valid output")
		}
	}
	return candidates[0].account, nil, nil, errors.New("competitive upstream returned no result")
}

func claudeUsageHasTokens(usage service.ClaudeUsage) bool {
	return usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 || usage.CacheCreation5mTokens > 0 ||
		usage.CacheCreation1hTokens > 0 || usage.ImageOutputTokens > 0
}

func competitiveAnthropicWasteAttempts(
	winnerID int,
	model string,
	candidates []competitiveAnthropicCandidate,
	outcomes map[int]competitiveAnthropicOutcome,
	reason service.CompetitiveWasteReason,
) []service.CompetitiveWasteAttempt {
	attempts := make([]service.CompetitiveWasteAttempt, 0, len(candidates))
	for id, candidate := range candidates {
		if id == winnerID || candidate.account == nil {
			continue
		}
		attempt := service.CompetitiveWasteAttempt{
			AttemptNo:             candidate.attemptNo,
			AccountID:             candidate.account.ID,
			AccountRateMultiplier: candidate.account.BillingRateMultiplier(),
			Model:                 model,
			Reason:                reason,
		}
		if outcome, ok := outcomes[id]; ok {
			attempt.Duration = outcome.duration
			if outcome.result != nil {
				attempt.RequestID = outcome.result.RequestID
				if outcome.result.Model != "" {
					attempt.Model = outcome.result.Model
				}
				attempt.UpstreamModel = outcome.result.UpstreamModel
				attempt.Usage = outcome.result.Usage
				attempt.UsageReported = claudeUsageHasTokens(outcome.result.Usage)
			}
		}
		attempts = append(attempts, attempt)
	}
	return attempts
}

func collectCompetitiveAnthropicWaste(
	winner *service.ForwardResult,
	winnerID int,
	model string,
	candidates []competitiveAnthropicCandidate,
	outcomes map[int]competitiveAnthropicOutcome,
	reason service.CompetitiveWasteReason,
) {
	if winner == nil {
		return
	}
	winner.CompetitiveWasteAttempts = append(
		winner.CompetitiveWasteAttempts,
		competitiveAnthropicWasteAttempts(winnerID, model, candidates, outcomes, reason)...,
	)
}

const pendingCompetitiveAnthropicWasteKey = "competitive_anthropic_pending_waste"

func rememberPendingCompetitiveAnthropicWaste(
	c *gin.Context,
	model string,
	candidates []competitiveAnthropicCandidate,
	outcomes map[int]competitiveAnthropicOutcome,
) {
	if c == nil {
		return
	}
	pending, _ := c.Get(pendingCompetitiveAnthropicWasteKey)
	attempts, _ := pending.([]service.CompetitiveWasteAttempt)
	attempts = append(
		attempts,
		competitiveAnthropicWasteAttempts(
			-1,
			model,
			candidates,
			outcomes,
			service.CompetitiveWasteReasonFailedBatch,
		)...,
	)
	if len(attempts) > 0 {
		c.Set(pendingCompetitiveAnthropicWasteKey, attempts)
	}
}

func mergePendingCompetitiveAnthropicWaste(c *gin.Context, result *service.ForwardResult) {
	if c == nil || result == nil {
		return
	}
	pending, ok := c.Get(pendingCompetitiveAnthropicWasteKey)
	if !ok {
		return
	}
	c.Set(pendingCompetitiveAnthropicWasteKey, nil)
	attempts, ok := pending.([]service.CompetitiveWasteAttempt)
	if ok {
		result.CompetitiveWasteAttempts = append(result.CompetitiveWasteAttempts, attempts...)
	}
}

func logCompetitiveAnthropicSettlementEvidence(reqLog *zap.Logger, winnerID int, startedAt time.Time, candidates []competitiveAnthropicCandidate, outcomes map[int]competitiveAnthropicOutcome, winner *service.ForwardResult) {
	if reqLog == nil {
		return
	}
	evidence := make([]map[string]any, 0, len(candidates))
	reportedWasteAttempts := 0
	unreportedWasteAttempts := 0
	for id, candidate := range candidates {
		isWinner := id == winnerID
		item := map[string]any{
			"attempt":    id + 1,
			"account_id": candidate.account.ID,
			"winner":     isWinner,
		}
		if outcome, ok := outcomes[id]; ok {
			item["duration_ms"] = outcome.duration.Milliseconds()
			reportedUsage := outcome.result != nil && claudeUsageHasTokens(outcome.result.Usage)
			item["reported_usage"] = reportedUsage
			if !isWinner {
				if reportedUsage {
					reportedWasteAttempts++
				} else {
					unreportedWasteAttempts++
				}
			}
			if outcome.err != nil {
				item["error"] = outcome.err.Error()
			}
			if !reportedUsage {
				item["settlement"] = "usage_unreported"
			}
		} else {
			item["reported_usage"] = false
			item["settlement"] = "usage_unreported"
			if !isWinner {
				unreportedWasteAttempts++
			}
		}
		evidence = append(evidence, item)
	}
	reqLog.Info("anthropic.competitive_concurrency.settlement_evidence",
		zap.Int("attempt_count", len(candidates)),
		zap.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
		zap.String("customer_usage_policy", "winner_only"),
		zap.Bool("winner_available", winner != nil),
		zap.Int("internal_waste_attempt_count", reportedWasteAttempts+unreportedWasteAttempts),
		zap.Int("reported_waste_usage_attempts", reportedWasteAttempts),
		zap.Int("unreported_waste_usage_attempts", unreportedWasteAttempts),
		zap.Any("attempts", evidence),
	)
}
