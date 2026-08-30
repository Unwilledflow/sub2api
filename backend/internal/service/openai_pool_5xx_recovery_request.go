package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
)

const openAIPool5xxRecoveryMaxBodyBytes int64 = 64 * 1024

func (s *AccountTestService) ProbeOpenAIPool5xxRecovery(ctx context.Context, account *Account, model string) (int, error) {
	if s == nil || s.httpUpstream == nil {
		return 0, errors.New("openai recovery probe transport is not configured")
	}
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey || !account.IsPoolMode() {
		return 0, errors.New("openai recovery probe account is not an API key pool account")
	}
	apiKey := strings.TrimSpace(account.GetOpenAIApiKey())
	if apiKey == "" {
		return 0, errors.New("openai recovery probe API key is empty")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return 0, errors.New("openai recovery probe model is empty")
	}
	baseURL := strings.TrimSpace(account.GetOpenAIBaseURL())
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return 0, fmt.Errorf("validate openai recovery probe base URL: %w", err)
	}

	var endpoint string
	var payload map[string]any
	if openai_compat.ShouldUseResponsesAPI(account.Extra) {
		endpoint = buildOpenAIResponsesURL(normalizedBaseURL)
		payload = map[string]any{
			"model":             model,
			"input":             "Reply with OK.",
			"max_output_tokens": 16,
			"stream":            false,
		}
	} else {
		endpoint = buildOpenAIChatCompletionsURL(normalizedBaseURL)
		payload = map[string]any{
			"model": model,
			"messages": []map[string]any{
				{"role": "user", "content": "Reply with OK."},
			},
			"max_tokens": 1,
			"stream":     false,
		}
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal openai recovery probe: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return 0, fmt.Errorf("build openai recovery probe: %w", err)
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	account.ApplyHeaderOverrides(req.Header)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	var resp *http.Response
	if s.tlsFPProfileService != nil {
		resp, err = s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	} else {
		resp, err = s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, nil)
	}
	if err != nil {
		return 0, fmt.Errorf("send openai recovery probe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, openAIPool5xxRecoveryMaxBodyBytes))
	if readErr != nil {
		return resp.StatusCode, fmt.Errorf("read openai recovery probe response: %w", readErr)
	}
	return resp.StatusCode, nil
}
