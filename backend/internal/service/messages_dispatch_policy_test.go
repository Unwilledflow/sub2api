//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayMessagesDispatchPolicyStopsAfterOneAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"messages", "passthrough", "chat", "responses"} {
		for _, policy := range []string{"default", "retry403", "disable403"} {
			t.Run(mode+"/"+policy, func(t *testing.T) {
				body := []byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`)
				if mode == "responses" {
					body = []byte(`{"model":"claude-opus-4-8","stream":true,"max_output_tokens":32,"input":"hi"}`)
				}
				errorBody := `{"error":{"type":"permission_error","message":"This group does not allow /v1/messages dispatch"}}`
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 403, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(errorBody))}}}
				repo := &rateLimitAccountRepoStub{}
				svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream, rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil), tlsFPProfileService: &TLSFingerprintProfileService{}}
				account := newAnthropicAPIKeyAccountForTest()
				account.Extra["anthropic_passthrough"] = mode == "passthrough"
				account.Credentials["pool_mode"] = true
				account.Credentials["pool_mode_retry_status_codes"] = []any{float64(403)}
				if policy != "default" {
					account.Credentials["custom_error_codes_enabled"] = true
					account.Credentials["custom_error_codes"] = []any{float64(401)}
					if policy == "disable403" {
						account.Credentials["custom_error_codes"] = []any{float64(403)}
					}
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))
				var result *ForwardResult
				var err error
				switch mode {
				case "chat":
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
				case "responses":
					result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
				default:
					parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, parseErr)
					result, err = svc.Forward(context.Background(), c, account, parsed)
				}
				require.Error(t, err)
				var failoverErr *UpstreamFailoverError
				require.NotErrorAs(t, err, &failoverErr)
				require.Nil(t, result)
				require.Equal(t, 1, upstream.callCount)
				require.Equal(t, 403, recorder.Code)
				require.Contains(t, recorder.Body.String(), "permission_error")
				require.Contains(t, recorder.Body.String(), "This group does not allow /v1/messages dispatch")
				require.Zero(t, repo.setErrorCalls)
				require.Zero(t, repo.tempCalls)
			})
		}
	}
}
