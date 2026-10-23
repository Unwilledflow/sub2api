//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCoolingFailoverMappingsRespectFailureKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixtures := []struct {
		name    string
		status  int
		body    []byte
		headers http.Header
		cooling bool
	}{
		{
			name: "capacity_400", status: http.StatusBadRequest, cooling: true,
			body: []byte(`{"error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}`),
		},
		{
			name: "messages_dispatch_policy", status: http.StatusForbidden,
			body: []byte(`{"error":{"code":"cooling","message":"This group does not allow /v1/messages dispatch"}}`),
		},
		{
			name: "cloudflare_header", status: http.StatusForbidden,
			body:    []byte(`{"error":{"message":"All candidates failed; providers are cooling down"}}`),
			headers: http.Header{"Cf-Mitigated": []string{"challenge"}},
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			failoverErr := &service.UpstreamFailoverError{StatusCode: fixture.status, ResponseBody: fixture.body, ResponseHeaders: fixture.headers}
			if fixture.name == "messages_dispatch_policy" {
				classification := service.ClassifyUpstreamFailure(fixture.status, fixture.headers, fixture.body, nil)
				require.Equal(t, service.UpstreamFailureClient, classification.Kind)
				require.False(t, classification.Failover)
				require.False(t, classification.SameAccountRetry)
			}
			handlers := []struct {
				name string
				run  func(*gin.Context)
			}{
				{"native_openai", func(c *gin.Context) { (&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failoverErr, false) }},
				{"anthropic_compat", func(c *gin.Context) {
					(&OpenAIGatewayHandler{}).handleAnthropicFailoverExhausted(c, failoverErr, false)
				}},
				{"chat_completions", func(c *gin.Context) { (&GatewayHandler{}).handleCCFailoverExhausted(c, failoverErr, false) }},
				{"responses_compat", func(c *gin.Context) { (&GatewayHandler{}).handleResponsesFailoverExhausted(c, failoverErr, false) }},
				{"messages", func(c *gin.Context) {
					(&GatewayHandler{}).handleFailoverExhausted(c, failoverErr, service.PlatformAnthropic, false)
				}},
				{"gemini", func(c *gin.Context) { (&GatewayHandler{}).handleGeminiFailoverExhausted(c, failoverErr) }},
			}
			for _, handler := range handlers {
				t.Run(handler.name, func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					handler.run(c)
					if fixture.cooling {
						require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
						require.Equal(t, "30", recorder.Header().Get("Retry-After"))
						require.Contains(t, recorder.Body.String(), "temporarily cooling down")
					} else {
						require.NotEqual(t, http.StatusServiceUnavailable, recorder.Code)
						require.Empty(t, recorder.Header().Get("Retry-After"))
						require.NotContains(t, recorder.Body.String(), "temporarily cooling down")
					}
				})
			}
		})
	}
}
