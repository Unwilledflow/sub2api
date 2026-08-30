package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Embeddings handles the OpenAI-compatible Embeddings API.
// POST /v1/embeddings
func (h *OpenAIGatewayHandler) Embeddings(c *gin.Context) {
	streamStarted := false
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
		"handler.openai_gateway.embeddings",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
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
	if !modelResult.Exists() || modelResult.Type != gjson.String || strings.TrimSpace(modelResult.String()) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	ensureCompositeTargetPlatform(c, apiKey, reqModel)
	if !compositeTargetPlatformAllowed(c, apiKey, reqModel, service.PlatformOpenAI) {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Model is not supported by this OpenAI-compatible endpoint for composite groups")
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel))
	setOpsRequestContext(c, reqModel, false)
	setOpsEndpointContext(c, "", int16(service.RequestTypeSync))
	if decision := h.checkSecurityAudit(c, reqLog, apiKey, subject, "openai_embeddings", reqModel, body); decision != nil && !decision.AllowNextStage {
		h.openAISecurityAuditError(c, decision)
		return
	}

	fallbackAttempts := h.buildOpenAIFallbackGroupAttempts(c.Request.Context(), apiKey, false, false, reqLog)

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, false, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	profitVetoCount := 0
	var lastFailoverErr *service.UpstreamFailoverError
	maxAccountSwitches := h.maxAccountSwitches
	routingStart := time.Now()

	// 分组利润控制：embeddings 文本入口请求级装门并固定 pricingAt。
	embPricingCtx, pricingAt := h.attachTextPricingContext(c, apiKey.GroupID)

	// 预扣：embeddings 按输入 token 计费，复用文本端点的 token 上限估算生命周期，
	// 在选号（上游产生费用）之前原子预留余额；结算走 RecordUsage → applyUsageBilling
	// 的 guard.Finalize 退实际差额，兜底 defer 退款在 worker 交接后自动失效。
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	preauthorizationBody := body
	if channelMapping.Mapped {
		preauthorizationBody = h.gatewayService.ReplaceModelInBody(body, channelMapping.MappedModel)
	}
	balanceGuard, err := preauthorizeTextGatewayRequest(
		embPricingCtx, h.balancePreauthorizer, h.gatewayService,
		apiKey, subscription, preauthorizationBody,
		service.BalancePreauthorizationBillingModel(reqModel, channelMapping),
		pricingAt, gjson.GetBytes(preauthorizationBody, "service_tier").String(),
	)
	if h.handlePreauthorizationError(c, err, false) {
		return
	}
	if balanceGuard != nil {
		defer deferBalancePreauthorizationRefund(reqLog, balanceGuard)
		c.Request = c.Request.WithContext(service.ContextWithBalancePreauthorizationGuard(c.Request.Context(), balanceGuard))
	}

	for attemptIndex, fallbackAttempt := range fallbackAttempts {
		if fallbackAttempt.Fallback {
			var ok bool
			fallbackAttempt, ok = h.resolveOpenAIFallbackGroupAttempt(c.Request.Context(), apiKey, fallbackAttempt, false, reqLog)
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
		nextFallbackGroupID := func() *int64 {
			return h.nextUsableOpenAIFallbackGroupID(c.Request.Context(), apiKey, fallbackAttempts, attemptIndex, false, false, attemptLog)
		}
		channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), attemptAPIKey.GroupID, reqModel)

		if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), attemptAPIKey.User, attemptAPIKey, attemptAPIKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), attemptAPIKey)); err != nil {
			attemptLog.Info("openai_embeddings.billing_check_failed", zap.Error(err))
			status, code, message, retryAfter := billingErrorDetails(err)
			if retryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(retryAfter))
			}
			h.errorResponse(c, status, code, message)
			return
		}

		failedAccountIDs := make(map[int64]struct{})
		switchCount := 0

		for {
			selection, _, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
				c.Request.Context(),
				attemptAPIKey.GroupID,
				"",
				"",
				reqModel,
				failedAccountIDs,
				service.OpenAIUpstreamTransportHTTPSSE,
				service.OpenAIEndpointCapabilityEmbeddings,
				false,
				false,
				true,
			)
			if err != nil {
				if failoverClientGone(c) {
					attemptLog.Info("openai_embeddings.account_select_aborted_client_disconnected", zap.Error(err))
					return
				}
				attemptLog.Warn("openai_embeddings.account_select_failed",
					zap.Error(err),
					zap.Int("excluded_account_count", len(failedAccountIDs)),
				)
				if len(failedAccountIDs) == 0 {
					if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
						attemptLog.Warn("openai_embeddings.group_failover_switching",
							zap.String("reason", "account_select_failed"),
							zap.Any("next_group_id", nextGroupID),
						)
						break
					}
					cls := classifyNoAccountErrorFromGin(c, h.gatewayService, attemptAPIKey, reqModel, reqModel, service.PlatformOpenAI)
					if !cls.ModelNotFound {
						markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
					}
					h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
					return
				}
				if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
					attemptLog.Warn("openai_embeddings.group_failover_switching",
						zap.String("reason", "account_switches_exhausted"),
						zap.Any("next_group_id", nextGroupID),
					)
					break
				}
				if lastFailoverErr != nil {
					h.handleFailoverExhausted(c, lastFailoverErr, false)
				} else {
					h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
				}
				return
			}
			if selection == nil || selection.Account == nil {
				if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
					attemptLog.Warn("openai_embeddings.group_failover_switching",
						zap.String("reason", "empty_selection"),
						zap.Any("next_group_id", nextGroupID),
					)
					break
				}
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, attemptAPIKey, reqModel, reqModel, service.PlatformOpenAI)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimited(c)
				}
				h.errorResponse(c, cls.Status, cls.ErrType, cls.Message)
				return
			}
			account := selection.Account
			setOpsSelectedAccount(c, account.ID, account.Platform)

			accountReleaseFunc, slotResult := h.acquireResponsesAccountSlot(c, attemptAPIKey.GroupID, "", selection, false, &streamStarted, attemptLog)
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
			writerSizeBeforeForward := c.Writer.Size()
			result, err := func() (*service.OpenAIForwardResult, error) {
				defer func() {
					if accountReleaseFunc != nil {
						accountReleaseFunc()
					}
				}()
				forwardCtx := h.withOpenAIFallbackResponseHeaderTimeout(c.Request.Context(), apiKey, fallbackAttempts, attemptIndex, false, false, attemptLog)
				return h.gatewayService.ForwardEmbeddings(forwardCtx, c, account, forwardBody, "")
			}()
			forwardDurationMs := time.Since(forwardStart).Milliseconds()
			upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
			responseLatencyMs := forwardDurationMs
			if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
				responseLatencyMs = forwardDurationMs - upstreamLatencyMs
			}
			service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)

			if err != nil {
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					if c.Writer.Size() != writerSizeBeforeForward {
						h.handleFailoverExhausted(c, failoverErr, true)
						return
					}
					h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), false, nil, err)
					h.gatewayService.ReportOpenAIAccountModelScheduleResult(account.ID, reqModel, false, nil)
					if failoverClientGone(c) {
						attemptLog.Info("openai_embeddings.failover_aborted_client_disconnected",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						return
					}
					if nextGroupID := nextFallbackGroupID(); nextGroupID != nil && shouldFastSwitchOpenAIFallbackGroup(failoverErr) {
						failedAccountIDs[account.ID] = struct{}{}
						lastFailoverErr = failoverErr
						attemptLog.Warn("openai_embeddings.group_failover_switching",
							zap.String("reason", "upstream_5xx"),
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
							zap.Any("next_group_id", nextGroupID),
						)
						break
					}
					failedAccountIDs[account.ID] = struct{}{}
					lastFailoverErr = failoverErr
					if maxAccountSwitches <= 0 || switchCount >= maxAccountSwitches {
						if nextGroupID := nextFallbackGroupID(); nextGroupID != nil {
							attemptLog.Warn("openai_embeddings.group_failover_switching",
								zap.String("reason", "account_switch_limit"),
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", failoverErr.StatusCode),
								zap.Any("next_group_id", nextGroupID),
							)
							break
						}
						h.handleFailoverExhausted(c, failoverErr, false)
						return
					}
					switchCount++
					h.gatewayService.RecordOpenAIAccountSwitch()
					attemptLog.Warn("openai_embeddings.upstream_failover_switching",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("switch_count", switchCount),
						zap.Int("max_switches", maxAccountSwitches),
					)
					continue
				}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), false, nil, err)
				h.gatewayService.ReportOpenAIAccountModelScheduleResult(account.ID, reqModel, false, nil)
				if failoverClientGone(c) {
					attemptLog.Info("openai_embeddings.forward_aborted_client_disconnected", zap.Int64("account_id", account.ID))
					return
				}
				if c.Writer.Size() == writerSizeBeforeForward {
					h.errorResponse(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
				}
				attemptLog.Warn("openai_embeddings.forward_failed",
					zap.Int64("account_id", account.ID),
					zap.Error(err),
				)
				return
			}

			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, reqModel, false, result), true, nil)
			h.gatewayService.ReportOpenAIAccountModelScheduleResult(account.ID, reqModel, true, nil)
			userAgent := c.GetHeader("User-Agent")
			clientIP := ip.GetClientIP(c)
			inboundEndpoint := GetInboundEndpoint(c)
			upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
			quotaPlatform := service.QuotaPlatform(c.Request.Context(), attemptAPIKey)
			sessionID := service.ExtractClientSessionID(c)

			h.gatewayService.RecordOpenAIUpstreamSuccess(c.Request.Context(), account, result, false)
			h.submitOpenAIUsageRecordTask(c.Request.Context(), result, nil, func(ctx context.Context) {
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
					ChannelUsageFields: clientRequestedUsageFields(c, channelMapping, reqModel, result.UpstreamModel),
					PricingAt:          pricingAt,
				}); err != nil {
					logger.L().With(
						zap.String("component", "handler.openai_gateway.embeddings"),
						zap.Int64("user_id", subject.UserID),
						zap.Int64("api_key_id", apiKey.ID),
						zap.Any("group_id", attemptAPIKey.GroupID),
						zap.String("model", reqModel),
						zap.Int64("account_id", account.ID),
					).Error("openai_embeddings.record_usage_failed", zap.Error(err))
				}
			})
			attemptLog.Debug("openai_embeddings.request_completed",
				zap.Int64("account_id", account.ID),
				zap.Int("switch_count", switchCount),
			)
			return
		}
	}
	if lastFailoverErr != nil {
		h.handleFailoverExhausted(c, lastFailoverErr, false)
	} else {
		h.errorResponse(c, http.StatusBadGateway, "api_error", "Upstream request failed")
	}
}
