package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type openAIPool5xxRecoveryHTTPStub struct {
	request *http.Request
	body    string
	status  int
}

func (s *openAIPool5xxRecoveryHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.DoWithTLS(req, "", 0, 0, nil)
}

func (s *openAIPool5xxRecoveryHTTPStub) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.request = req
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	s.body = string(body)
	return &http.Response{
		StatusCode: s.status,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Header:     make(http.Header),
	}, nil
}

func TestAccountTestService_ProbeOpenAIPool5xxRecoveryUsesAccountProtocol(t *testing.T) {
	tests := []struct {
		name      string
		extra     map[string]any
		wantPath  string
		wantField string
	}{
		{name: "responses", wantPath: "/v1/responses", wantField: `"max_output_tokens":16`},
		{name: "chat_completions", extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}, wantPath: "/v1/chat/completions", wantField: `"max_tokens":1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &openAIPool5xxRecoveryHTTPStub{status: http.StatusOK}
			svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
			account := &Account{
				ID:       42,
				Platform: PlatformOpenAI,
				Type:     AccountTypeAPIKey,
				Extra:    tt.extra,
				Credentials: map[string]any{
					"api_key":   "secret",
					"base_url":  "https://upstream.example",
					"pool_mode": true,
				},
			}

			status, err := svc.ProbeOpenAIPool5xxRecovery(context.Background(), account, "gpt-5.5")

			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			require.Equal(t, tt.wantPath, upstream.request.URL.Path)
			require.Equal(t, "Bearer secret", upstream.request.Header.Get("Authorization"))
			require.Contains(t, upstream.body, `"model":"gpt-5.5"`)
			require.Contains(t, upstream.body, tt.wantField)
			require.Contains(t, upstream.body, `"stream":false`)
		})
	}
}
