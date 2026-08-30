package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ChatCompletions handles OpenAI Chat Completions API requests.
// POST /v1/chat/completions
func (h *OpenAIGatewayHandler) ChatCompletions(c *gin.Context) {
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.chat_completions",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		logRequestBodyReadFailure(reqLog, c.Request, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if !gjson.ValidBytes(body) {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	ensureCompositeTargetPlatform(c, apiKey, reqModel)
	if !openAICompatibleTextTargetAllowed(c, apiKey, reqModel) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return
	}
	if cappedBody, changed := applyOpenAIReasoningEffortPolicyForRequest(c, apiKey, body); changed {
		body = cappedBody
	}
	schedulerPerformanceClass := service.OpenAISchedulerPerformanceClass(body, reqModel)
	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage)
		return
	}
	if _, err := service.ValidateOpenAIServiceTierField(body); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if service.IsGPTImageGenerationModel(reqModel) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "This model is not supported on the Chat Completions endpoint")
		return
	}

	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))

	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIChat, reqModel, body); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}
	if h.rejectIfCyberSessionBlocked(c, apiKey, body, reqModel, cyberBlockFormatChat) {
		return
	}

	// 解析渠道级模型映射
	fallbackAttempts := h.buildOpenAIGroupAttempts(c, apiKey, reqModel, service.AdaptiveRouteProtocolOpenAIChat, true, false, reqLog)
	defer h.finishAdaptiveOpenAIRequest(c.Request.Context(), c, "request_end", reqLog)

	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	sessionHash := h.gatewayService.GenerateSessionHash(c, body)
	promptCacheKey := h.gatewayService.ExtractSessionID(c, body)

	maxAccountSwitches := h.maxAccountSwitches
	profitVetoCount := 0
	var lastFailoverErr *service.UpstreamFailoverError
	oauth429FailoverState := service.OpenAIOAuth429FailoverState{FillScheduling: true}

	// 分组利润控制：chat completions 文本入口请求级装门并固定 pricingAt。
	ccPricingCtx := c.Request.Context()
	ccPricingCtx = h.gatewayService.WithCodexQuotaOverdraftScheduling(ccPricingCtx)
	ccPricingCtx, pricingAt := h.gatewayService.WithOpenAIRequestPricingContext(ccPricingCtx, apiKey.GroupID)
	c.Request = c.Request.WithContext(ccPricingCtx)
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	preauthorizationBody := openAIModelMappedBody(body, channelMapping.Mapped, channelMapping.MappedModel, h.gatewayService.ReplaceModelInBody)
	balanceGuard, err := preauthorizeTextGatewayRequest(
		ccPricingCtx, h.balancePreauthorizer, h.gatewayService,
		apiKey, subscription, preauthorizationBody,
		service.BalancePreauthorizationBillingModel(reqModel, channelMapping),
		pricingAt, gjson.GetBytes(preauthorizationBody, "service_tier").String(),
	)
	if err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}
	if balanceGuard != nil {
		defer deferBalancePreauthorizationRefund(reqLog, balanceGuard)
		c.Request = c.Request.WithContext(service.ContextWithBalancePreauthorizationGuard(c.Request.Context(), balanceGuard))
	}
	// Keep a proxied streaming request alive even while the selected upstream
	// is still waiting to return response headers.  The manager is a no-op for
	// non-streaming JSON and shares the semantic-byte accounting used by
	// failover, so comments never suppress a safe account switch.
	stopStreamHeaderKeepalive := func() {}
	if reqStream {
		stopStreamHeaderKeepalive = service.StartOpenAIStreamSSEKeepalive(c, h.openAICompactKeepaliveInterval())
	}
	defer stopStreamHeaderKeepalive()

	for attemptIndex, fallbackAttempt := range fallbackAttempts {
		if fallbackAttempt.Fallback {
			var ok bool
			fallbackAttempt, ok = h.resolveOpenAIFallbackGroupAttempt(c.Request.Context(), apiKey, fallbackAttempt, true, reqLog)
			if !ok {
				fallbackAttempt.Skipped = true
				fallbackAttempts[attemptIndex] = fallbackAttempt
				continue
			}
			fallbackAttempts[attemptIndex] = fallbackAttempt
		}
		attemptAPIKey := fallbackAttempt.APIKey
		if attemptAPIKey == nil {
			attemptAPIKey = apiKey
		}
		attemptLog := reqLog.With(
			zap.Any("group_id", attemptAPIKey.GroupID),
			zap.Bool("fallback_group", fallbackAttempt.Fallback),
			zap.Int("fallback_index", attemptIndex),
		)
		if attemptAPIKey.GroupID != nil {
			if !h.markAdaptiveLeafInFlight(c.Request.Context(), c, *attemptAPIKey.GroupID, attemptIndex+1, attemptLog) {
				// H2 fail-closed: the reservation's in-flight fence could not be
				// persisted. Skipping this attempt (no upstream work) prevents an
				// uncapturable pending usage row that would strand the hold in
				// reconciling forever; the finish path releases the unused hold.
				attemptLog.Warn("openai_chat_completions.adaptive_attempt_aborted_no_in_flight_fence",
					zap.Int64("group_id", *attemptAPIKey.GroupID),
					zap.Int("attempt_index", attemptIndex),
				)
				continue
			}
		}
		nextFallbackGroupID := func() *int64 {
			if sess := getOpenAIAdaptiveSession(c); sess != nil {
				if streamStarted || c.Writer.Written() {
					return nil
				}
			}
			return h.nextUsableOpenAIFallbackGroupID(c.Request.Context(), apiKey, fallbackAttempts, attemptIndex, true, false, attemptLog)
		}
		channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), attemptAPIKey.GroupID, reqModel)
		requestPlatform := openAICompatibleRequestPlatform(c.Request.Context(), attemptAPIKey)

		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), attemptAPIKey.User, attemptAPIKey, attemptAPIKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), attemptAPIKey)); err != nil {
			attemptLog.Info("openai_chat_completions.billing_eligibility_check_failed", zap.Error(err))
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.handleStreamingAwareError(c, status, code, message, streamStarted)
			return
		}

		switchCount := 0
		failedAccountIDs := make(map[int64]struct{})
		sameAccountRetryCount := make(map[int64]int)

		for {
			if failoverClientGone(c) {
				return
			}
			attemptLog.Debug("openai_chat_completions.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
			selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForCapabilityClass(
				c.Request.Context(),
				attemptAPIKey.GroupID,
				"",
				sessionHash,
				reqModel,
				failedAccountIDs,
				service.OpenAIUpstreamTransportAny,
				service.OpenAIEndpointCapabilityChatCompletions,
				false,
				false,
				true,
				schedulerPerformanceClass,
				requestPlatform,
			)
			if err != nil {
				if failoverClientGone(c) {
					attemptLog.Info("openai_chat_completions.account_select_aborted_client_disconnected", zap.Error(err))
					return
				}
				attemptLog.Warn("openai_chat_completions.account_select_failed",
					zap.Error(openAICompatibleSelectionErrorForLog(err, requestPlatform)),
					zap.Int("excluded_account_count", len(failedAccountIDs)),
				)
				if len(failedAccountIDs) == 0 {
					if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
						h.markAdaptiveLeafFailed(c.Request.Context(), c, "account_select_failed", attemptLog)
						attemptLog.Warn("openai_chat_completions.group_failover_switching",
							zap.String("reason", "account_select_failed"),
							zap.Any("next_group_id", nextGroupID),
						)
						break
					}
					cls := classifyOpenAICompatibleNoAccountErrorFromGin(c, h.gatewayService, attemptAPIKey, reqModel, reqModel)
					cls = classifySelectionFailureErrorFromGin(c, err, cls)
					if !cls.ModelNotFound {
						markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
					}
					h.handleStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
					return
				} else {
					if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
						h.markAdaptiveLeafFailed(c.Request.Context(), c, "account_switches_exhausted", attemptLog)
						attemptLog.Warn("openai_chat_completions.group_failover_switching",
							zap.String("reason", "account_switches_exhausted"),
							zap.Any("next_group_id", nextGroupID),
						)
						break
					}
					if lastFailoverErr != nil {
						h.markAdaptiveLeafFailed(c.Request.Context(), c, "account_switches_exhausted", attemptLog)
						h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
					} else {
						h.handleStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
					}
					return
				}
			}
			if selection == nil || selection.Account == nil {
				if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
					h.markAdaptiveLeafFailed(c.Request.Context(), c, "empty_selection", attemptLog)
					attemptLog.Warn("openai_chat_completions.group_failover_switching",
						zap.String("reason", "empty_selection"),
						zap.Any("next_group_id", nextGroupID),
					)
					break
				}
				cls := classifyOpenAICompatibleNoAccountErrorFromGin(c, h.gatewayService, attemptAPIKey, reqModel, reqModel)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimited(c)
				}
				h.handleStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
				return
			}
			account := selection.Account
			sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
			attemptLog.Debug("openai_chat_completions.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
			_ = scheduleDecision
			setOpsSelectedAccount(c, account.ID, account.Platform)

			accountReleaseFunc, slotResult := h.acquireResponsesAccountSlot(c, attemptAPIKey.GroupID, sessionHash, selection, reqStream, &streamStarted, attemptLog)
			if slotResult == openAISlotAcquireProfitVetoed {
				// 利润终检否决：排除该账号重新选号；否决次数达上限则按无可用账号终止。
				if !recordOpenAIProfitVeto(failedAccountIDs, account.ID, &profitVetoCount) {
					h.handleOpenAIProfitVetoExhausted(c, streamStarted, attemptLog, profitVetoCount)
					return
				}
				continue
			}
			if slotResult != openAISlotAcquireOK {
				return
			}

			service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
			forwardStart := time.Now()

			forwardBody := body
			if channelMapping.Mapped {
				forwardBody = h.gatewayService.ReplaceModelInBody(body, channelMapping.MappedModel)
			}
			// Keep transport-only SSE comments out of the semantic-output
			// failover decision.  The pre-header manager may have committed one
			// or more comments while account selection/slot acquisition waited.
			writerSizeBeforeForward := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)
			var result *service.OpenAIForwardResult
			if groupCompetitiveConcurrencyEnabled(attemptAPIKey) {
				selectNext := func(ctx context.Context, excludedIDs map[int64]struct{}) (*service.AccountSelectionResult, error) {
					next, _, selectErr := h.gatewayService.SelectAccountWithSchedulerForCapabilityClass(
						ctx, attemptAPIKey.GroupID, "", sessionHash, reqModel, excludedIDs,
						service.OpenAIUpstreamTransportAny, service.OpenAIEndpointCapabilityChatCompletions,
						false, false, true, schedulerPerformanceClass, requestPlatform,
					)
					return next, selectErr
				}
				forward := func(ctx context.Context, attemptGin *gin.Context, candidate *service.Account) (*service.OpenAIForwardResult, error) {
					forwardCtx := h.withOpenAIFallbackResponseHeaderTimeout(ctx, apiKey, fallbackAttempts, attemptIndex, true, false, attemptLog)
					return h.gatewayService.ForwardAsChatCompletions(forwardCtx, attemptGin, candidate, forwardBody, promptCacheKey, "")
				}
				account, result, err = h.runCompetitiveOpenAIForward(
					c, attemptAPIKey.GroupID, sessionHash, selection, accountReleaseFunc, failedAccountIDs,
					reqModel, schedulerPerformanceClass, selectNext, forward, attemptLog,
				)
				if account != nil {
					setOpsSelectedAccount(c, account.ID, account.Platform)
				}
			} else {
				result, err = func() (*service.OpenAIForwardResult, error) {
					defer func() {
						if accountReleaseFunc != nil {
							accountReleaseFunc()
						}
					}()
					forwardCtx := h.withOpenAIFallbackResponseHeaderTimeout(c.Request.Context(), apiKey, fallbackAttempts, attemptIndex, true, false, attemptLog)
					return h.gatewayService.ForwardAsChatCompletions(forwardCtx, c, account, forwardBody, promptCacheKey, "")
				}()
			}
			var cyberBlockBodyChat []byte
			if service.GetOpsCyberPolicy(c) != nil {
				cyberBlockBodyChat = body
			}
			h.recordCyberPolicyIfMarked(c, attemptAPIKey, account, subscription, reqModel, shouldRecordStandaloneCyberUsage(err, result != nil), cyberBlockBodyChat, clientRequestedUsageFields(c, channelMapping, reqModel, ""), service.HashUsageRequestPayload(body))

			forwardDurationMs := time.Since(forwardStart).Milliseconds()
			upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
			responseLatencyMs := forwardDurationMs
			if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
				responseLatencyMs = forwardDurationMs - upstreamLatencyMs
			}
			service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
			if err == nil && result != nil && result.FirstTokenMs != nil {
				service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
			}
			if err != nil {
				if result != nil && result.ImageCount > 0 {
					attemptLog.Warn("openai_chat_completions.forward_partial_error_with_image_result",
						zap.Int64("account_id", account.ID),
						zap.Int("image_count", result.ImageCount),
						zap.Error(err),
					)
				} else {
					var failoverErr *service.UpstreamFailoverError
					if errors.As(err, &failoverErr) {
						if failoverClientGone(c) {
							attemptLog.Info("openai_chat_completions.failover_aborted_client_disconnected",
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", failoverErr.StatusCode),
							)
							return
						}
						if c.Writer.Size() != writerSizeBeforeForward {
							h.handleFailoverExhausted(c, failoverErr, true)
							return
						}
					if failoverErr.ShouldReportAccountScheduleFailure() {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, nil), false, nil, err)
						h.gatewayService.ReportOpenAIAccountModelScheduleResultWithClass(account.ID, reqModel, schedulerPerformanceClass, false, nil)
					}
						if !failoverErr.ShouldRetryNextAccount() {
							h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("non_retryable_upstream_%d", failoverErr.StatusCode), attemptLog)
							h.handleFailoverExhausted(c, failoverErr, streamStarted)
							return
						}
						if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
							antiOwnsFailure := false
							if anti := service.AntiStallSessionFromGin(c); anti != nil && anti.Config().Enabled {
								// Preserve production settlement ordering: mark the failed
								// attempt before Anti-stall enters recovery.
								h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("upstream_%d", failoverErr.StatusCode), attemptLog)
								antiOwnsFailure = true
							}
							decision := h.decideAdaptiveLeafFailure(c, apiKey, failoverErr, nextGroupID)
							switchLeaf := false
							switch decision.Action {
							case adaptiveLeafFailureHold:
								lastFailoverErr = failoverErr
								failedAccountIDs = make(map[int64]struct{})
								attemptLog.Info("openai_chat_completions.adaptive_leaf_held",
									zap.String("owner", decision.Owner),
									zap.Int("failure_count", decision.FailureCount),
									zap.Int("switch_threshold", decision.SwitchThreshold),
									zap.Int64("account_id", account.ID),
									zap.Int("upstream_status", failoverErr.StatusCode),
								)
								continue
							case adaptiveLeafFailureFailHard:
								h.handleFailoverExhausted(c, failoverErr, streamStarted)
								return
							case adaptiveLeafFailureSwitch:
								switchLeaf = true
								failedAccountIDs[account.ID] = struct{}{}
								lastFailoverErr = failoverErr
								if !antiOwnsFailure {
									h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("upstream_%d", failoverErr.StatusCode), attemptLog)
								}
								attemptLog.Warn("openai_chat_completions.group_failover_switching",
									zap.String("reason", decision.Owner+"_upstream_failover"),
									zap.Int("failure_count", decision.FailureCount),
									zap.Bool("force_leaf", decision.Forced),
									zap.Int64("account_id", account.ID),
									zap.Int("upstream_status", failoverErr.StatusCode),
									zap.Any("next_group_id", nextGroupID),
								)
							default:
								if openAIShouldGroupFailover(c, failoverErr) {
									switchLeaf = true
									failedAccountIDs[account.ID] = struct{}{}
									lastFailoverErr = failoverErr
									h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("upstream_%d", failoverErr.StatusCode), attemptLog)
									attemptLog.Warn("openai_chat_completions.group_failover_switching",
										zap.String("reason", "upstream_failover"),
										zap.Int64("account_id", account.ID),
										zap.Int("upstream_status", failoverErr.StatusCode),
										zap.Any("next_group_id", nextGroupID),
									)
								}
							}
							if switchLeaf {
								break
							}
						}
						// Pool mode: retry on the same account
						if failoverErr.RetryableOnSameAccount {
							retryLimit := account.GetPoolModeRetryCount()
							if sameAccountRetryCount[account.ID] < retryLimit {
								sameAccountRetryCount[account.ID]++
								retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
								attemptLog.Warn("openai_chat_completions.pool_mode_same_account_retry",
									zap.Int64("account_id", account.ID),
									zap.Int("upstream_status", failoverErr.StatusCode),
									zap.Int("retry_limit", retryLimit),
									zap.Int("retry_count", sameAccountRetryCount[account.ID]),
									zap.Duration("retry_delay", retryDelay),
								)
								select {
								case <-c.Request.Context().Done():
									return
								case <-time.After(retryDelay):
								}
								continue
							}
						}
						failedAccountIDs[account.ID] = struct{}{}
						lastFailoverErr = failoverErr
						if maxAccountSwitches <= 0 || switchCount >= maxAccountSwitches {
							if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
								h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("upstream_%d", failoverErr.StatusCode), attemptLog)
								attemptLog.Warn("openai_chat_completions.group_failover_switching",
									zap.String("reason", "account_switch_limit"),
									zap.Int64("account_id", account.ID),
									zap.Int("upstream_status", failoverErr.StatusCode),
									zap.Any("next_group_id", nextGroupID),
								)
								break
							}
							// All leaf groups exhausted: mark final attempt failed before returning error
							h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("all_leaves_exhausted_upstream_%d", failoverErr.StatusCode), attemptLog)
							h.handleFailoverExhausted(c, failoverErr, streamStarted)
							return
						}
						switchCount++
						if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
							if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
								if anti := service.AntiStallSessionFromGin(c); anti != nil && anti.Config().Enabled && !anti.ShouldFailHard() {
									h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("upstream_%d", failoverErr.StatusCode), attemptLog)
									anti.RecordLeafSwitch()
									service.EnsureAntiStallDripRunning(c, anti)
									attemptLog.Warn("openai_chat_completions.anti_stall_pro_429_leaf_switch",
										zap.Int64("account_id", account.ID),
										zap.Int("leaf_switches", anti.LeafSwitches()),
										zap.Any("next_group_id", nextGroupID),
									)
									break
								}
							}
							// OAuth 429 failover stopped: mark final attempt failed before returning error
							h.markAdaptiveLeafFailed(c.Request.Context(), c, fmt.Sprintf("oauth_429_stopped_upstream_%d", failoverErr.StatusCode), attemptLog)
							h.handleFailoverExhausted(c, failoverErr, streamStarted)
							return
						}
						h.gatewayService.RecordOpenAIAccountSwitch()
						attemptLog.Warn("openai_chat_completions.upstream_failover_switching",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
							zap.Int("switch_count", switchCount),
							zap.Int("max_switches", maxAccountSwitches),
						)
						continue
					}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, nil), false, nil, err)
				h.gatewayService.ReportOpenAIAccountModelScheduleResultWithClass(account.ID, reqModel, schedulerPerformanceClass, false, nil)
				upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
					wroteFallback := false
					if !upstreamErrorAlreadyCommunicated {
						wroteFallback = h.ensureOpenAIStreamReadErrorResponse(c, err, streamStarted)
						if !wroteFallback {
							wroteFallback = h.ensureForwardErrorResponse(c, streamStarted)
						}
					}
					attemptLog.Warn("openai_chat_completions.forward_failed",
						zap.Int64("account_id", account.ID),
						zap.Bool("fallback_error_response_written", wroteFallback),
						zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
						zap.Error(err),
					)
					return
				}
			}
			if result != nil {
				mergePendingCompetitiveOpenAIUsage(c, result)
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), true, result.FirstTokenMs)
				h.gatewayService.ReportOpenAIAccountModelScheduleResultWithClass(account.ID, reqModel, schedulerPerformanceClass, true, result.FirstTokenMsForScheduling())
			} else {
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), true, nil)
				h.gatewayService.ReportOpenAIAccountModelScheduleResultWithClass(account.ID, reqModel, schedulerPerformanceClass, true, nil)
			}

			userAgent := c.GetHeader("User-Agent")
			clientIP := ip.GetClientIP(c)
			inboundEndpoint := GetInboundEndpoint(c)
			upstreamEndpoint := resolveOpenAIUpstreamEndpoint(c, account, result)
			quotaPlatform := service.QuotaPlatform(c.Request.Context(), attemptAPIKey)
			sessionID := service.ExtractClientSessionID(c)

			cyberBlocked := service.GetOpsCyberPolicy(c) != nil
			adaptiveBillingCtx, adaptiveSession := prepareAdaptiveSessionSettlement(c)
			channelUsageFields := clientRequestedUsageFields(c, channelMapping, reqModel, result.UpstreamModel)
			if result != nil {
				ttft, total := adaptiveTimingsFromOpenAIResult(result)
				h.recordAdaptiveLeafSignalSuccess(c, ttft, total)
			}
			probe := adaptiveBillingCtx != nil && adaptiveBillingCtx.Probe
			h.gatewayService.RecordOpenAIUpstreamSuccess(c.Request.Context(), account, result, cyberBlocked || probe)
			h.submitOpenAIUsageRecordTask(c.Request.Context(), result, adaptiveSession, func(ctx context.Context) {
				if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
					Result:             result,
					APIKey:             attemptAPIKey,
					User:               attemptAPIKey.User,
					Account:            account,
					Subscription:       subscription,
					InboundEndpoint:    inboundEndpoint,
					UpstreamEndpoint:   upstreamEndpoint,
					UserAgent:          userAgent,
					IPAddress:          clientIP,
					APIKeyService:      h.apiKeyService,
					QuotaPlatform:      quotaPlatform,
					SessionID:          sessionID,
					ChannelUsageFields: channelUsageFields,
					PricingAt:          pricingAt,
					CyberBlocked:       cyberBlocked,
					AdaptiveBilling:    adaptiveBillingCtx,
				}); err != nil {
					logger.L().With(
						zap.String("component", "handler.openai_gateway.chat_completions"),
						zap.Int64("user_id", subject.UserID),
						zap.Int64("api_key_id", apiKey.ID),
						zap.Any("group_id", attemptAPIKey.GroupID),
						zap.String("model", reqModel),
						zap.Int64("account_id", account.ID),
					).Error("openai_chat_completions.record_usage_failed", zap.Error(err))
					return
				}
				adaptiveSession.markAdaptiveBillingCaptured()
			})
			attemptLog.Debug("openai_chat_completions.request_completed",
				zap.Int64("account_id", account.ID),
				zap.Int("switch_count", switchCount),
			)
			if result != nil {
				h.gatewayService.ObserveCodexQuotaOverdraftScheduleSuccess(c.Request.Context(), account, reqModel)
			}
			return
		}
	}
	if lastFailoverErr != nil {
		h.markAdaptiveLeafFailed(c.Request.Context(), c, "all_fallback_attempts_exhausted", reqLog)
		h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
	} else {
		h.handleStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
	}
}

// getNextLeafHealthScore looks up the health score of the next leaf group from the Adaptive plan.
func (h *OpenAIGatewayHandler) getNextLeafHealthScore(c *gin.Context, nextLeafGroupID *int64) float64 {
	if nextLeafGroupID == nil {
		return 0.0
	}
	session := getOpenAIAdaptiveSession(c)
	if session == nil || session.Plan == nil {
		return 0.0
	}
	// Look up health score from the plan's candidates
	candidates := session.Plan.Candidates()
	for _, attempt := range candidates {
		if attempt.LeafGroupID == *nextLeafGroupID {
			return attempt.HealthScore
		}
	}
	return 0.0
}

// resolveOpenAIUpstreamEndpoint returns the actual upstream endpoint for an
// OpenAI-compatible account. A forwarding result is authoritative because a
// single inbound route may choose raw Chat or a Responses bridge at runtime.
// The account-based derivation remains as a fallback for existing callers and
// forwarding paths that do not report their endpoint yet.
func resolveOpenAIUpstreamEndpoint(c *gin.Context, account *service.Account, result *service.OpenAIForwardResult) string {
	if result != nil {
		if endpoint := strings.TrimSpace(result.UpstreamEndpoint); endpoint != "" {
			return endpoint
		}
	}
	if endpoint := service.GetActualOpenAIUpstreamEndpoint(c); endpoint != "" {
		return endpoint
	}
	if account != nil && account.Type == service.AccountTypeAPIKey &&
		!openai_compat.ShouldUseResponsesAPI(account.Extra) {
		return EndpointChatCompletions
	}
	return GetUpstreamEndpoint(c, account.Platform)
}
