package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// CodexModels serves the Codex models manifest for Codex clients.
//
// Codex CLI and the Codex desktop app refresh their model picker from
// GET {base_url}/models?client_version=... (custom provider mode) or
// GET /backend-api/codex/models (chatgpt_base_url mode). Both routes land
// here. ChatGPT manifests are proxied verbatim; custom API key manifests receive
// provider-compatibility normalization and use a short-lived, asynchronously
// revalidated cache to tolerate canceled client requests.
func (h *OpenAIGatewayHandler) CodexModels(c *gin.Context) {
	if c.Request.Context().Err() != nil {
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		h.errorResponse(c, http.StatusUnauthorized, "invalid_request_error", "API key group is required")
		return
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Codex models manifest is only available for OpenAI and Composite groups")
		return
	}

	maxAccountSwitches := h.maxAccountSwitches
	if maxAccountSwitches <= 0 {
		maxAccountSwitches = 3
	}
	failedAccountIDs := make(map[int64]struct{})
	switchCount := 0
	var lastUpstreamErr error

	// Adaptive 父组本身不直接绑定账号；其可调度账号在叶子组里。
	// 先取一次叶子组尝试链（与 /responses 同一 Planner 排序），逐叶子选账号，
	// 失败再回退到按父组直接选（legacy 普通组路径）。
	groupAttempts := h.codexModelsGroupAttempts(c, apiKey)

	for {
		account, err := h.gatewayService.SelectAccountForModelWithExclusions(c.Request.Context(), apiKey.GroupID, "", "", failedAccountIDs)
		if err != nil || account == nil {
			account, err = h.selectCodexModelsAccountFromLeafGroups(c.Request.Context(), apiKey, groupAttempts, failedAccountIDs)
		}
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if lastUpstreamErr != nil {
				h.errorResponse(c, infraerrors.Code(lastUpstreamErr), "upstream_error", infraerrors.Message(lastUpstreamErr))
				return
			}
			h.errorResponse(c, http.StatusServiceUnavailable, "upstream_error", "No available OpenAI accounts")
			return
		}
		// 让 ops 错误日志携带实际选中的上游账号，便于定位失效账号（#4544）。
		setOpsSelectedAccount(c, account.ID, account.Platform)

		manifest, err := h.gatewayService.FetchCodexModelsManifest(c.Request.Context(), account, c.Query("client_version"), c.GetHeader("If-None-Match"))
		if err != nil {
			if c.Request.Context().Err() != nil {
				return
			}
			if service.IsRetryableCodexModelsManifestError(err) && fillSchedulingSwitchAllowed(switchCount, maxAccountSwitches) {
				failedAccountIDs[account.ID] = struct{}{}
				switchCount++
				lastUpstreamErr = err
				continue
			}
			h.errorResponse(c, infraerrors.Code(err), "upstream_error", infraerrors.Message(err))
			return
		}
		if c.Request.Context().Err() != nil {
			return
		}

		if manifest.ETag != "" {
			c.Header("ETag", manifest.ETag)
		}
		if manifest.NotModified {
			c.Status(http.StatusNotModified)
			return
		}
		c.Data(http.StatusOK, "application/json", manifest.Body)
		return
	}
}

// codexModelsGroupAttempts builds the adaptive leaf attempt chain for the
// Codex models manifest. For non-adaptive keys it returns nil.
func (h *OpenAIGatewayHandler) codexModelsGroupAttempts(c *gin.Context, apiKey *service.APIKey) []openAIFallbackGroupAttempt {
	if h == nil || apiKey == nil || apiKey.GroupID == nil {
		return nil
	}
	if h.cfg == nil || !h.cfg.Gateway.AdaptiveRoutingEnabled || h.adaptivePlanner == nil {
		return nil
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	session, ok := h.startAdaptiveOpenAISession(ctx, c, apiKey, "", service.AdaptiveRouteProtocolOpenAIResponses, true, nil)
	if !ok || session == nil {
		return nil
	}
	return session.Attempts
}

// selectCodexModelsAccountFromLeafGroups walks the adaptive leaf groups and
// returns the first selectable account, honoring per-request exclusions.
func (h *OpenAIGatewayHandler) selectCodexModelsAccountFromLeafGroups(ctx context.Context, apiKey *service.APIKey, attempts []openAIFallbackGroupAttempt, excludedIDs map[int64]struct{}) (*service.Account, error) {
	for _, attempt := range attempts {
		if attempt.GroupID == nil || *attempt.GroupID <= 0 {
			continue
		}
		leafGroupID := *attempt.GroupID
		account, err := h.gatewayService.SelectAccountForModelWithExclusions(ctx, &leafGroupID, "", "", excludedIDs)
		if err != nil || account == nil {
			continue
		}
		return account, nil
	}
	return nil, infraerrors.New(infraerrors.Code(service.ErrNoAvailableAccounts), "upstream_error", service.ErrNoAvailableAccounts.Error())
}
