package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLogOpenAISlowTTFTDiagnosticLogsShapeWithoutSensitiveContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logSink, restore := captureStructuredLog(t)
	defer restore()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.5 (Windows 10.0.26100; x86_64)")
	c.Set(OpsAuthLatencyMsKey, int64(35))
	c.Set(OpsRoutingLatencyMsKey, int64(75))
	c.Set("api_key", &APIKey{
		ID:      210,
		UserID:  103,
		Key:     "sk-sensitive-api-key",
		GroupID: slowTTFTInt64Ptr(17),
	})
	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"reasoning":{"effort":"high"},"prompt_cache_key":"sensitive-cache-key","input":[{"type":"message","content":"sensitive prompt text"},{"type":"function_call_output","output":"sensitive tool output"}],"tools":[{"type":"function","name":"shell","description":"sensitive description"}]}`)
	timing := openAIStreamTiming{
		forwardToResponseHeaderMs: 180,
		firstUpstreamEventMs:      240,
		firstUpstreamEventType:    "response.created",
		firstSemanticEventMs:      12340,
		firstSemanticEventType:    "response.reasoning_summary_text.delta",
	}

	logOpenAISlowTTFTDiagnostic(
		context.Background(),
		c,
		&Account{ID: 339, Name: "account-name", Platform: PlatformOpenAI},
		body,
		"gpt-5.6-sol",
		125,
		timing,
	)

	require.True(t, logSink.ContainsMessageAtLevel("OpenAI passthrough slow first output", "warn"))
	require.True(t, logSink.ContainsFieldValue("user_id", "103"))
	require.True(t, logSink.ContainsFieldValue("api_key_id", "210"))
	require.True(t, logSink.ContainsFieldValue("group_id", "17"))
	require.True(t, logSink.ContainsFieldValue("account_id", "339"))
	require.True(t, logSink.ContainsFieldValue("request_body_bytes", fmt.Sprint(len(body))))
	require.True(t, logSink.ContainsFieldValue("input_item_count", "2"))
	require.True(t, logSink.ContainsFieldValue("tool_count", "1"))
	require.True(t, logSink.ContainsFieldValue("prompt_cache_key_present", "true"))
	require.True(t, logSink.ContainsFieldValue("reasoning_effort", "high"))
	require.True(t, logSink.ContainsFieldValue("request_class", "effort=high;tier=default;context=small;chain=standalone"))
	require.True(t, logSink.ContainsFieldValue("response_header_ms", "125"))
	require.True(t, logSink.ContainsFieldValue("forward_to_response_header_ms", "180"))
	require.True(t, logSink.ContainsFieldValue("first_upstream_event_ms", "240"))
	require.True(t, logSink.ContainsFieldValue("response_header_to_first_event_ms", "60"))
	require.True(t, logSink.ContainsFieldValue("first_upstream_event_type", "response.created"))
	require.True(t, logSink.ContainsFieldValue("first_semantic_event_ms", "12340"))
	require.True(t, logSink.ContainsFieldValue("first_event_to_semantic_ms", "12100"))
	require.True(t, logSink.ContainsFieldValue("first_semantic_event_type", "response.reasoning_summary_text.delta"))
	require.True(t, logSink.ContainsFieldValue("request_user_agent", "0.144.5"))
	require.True(t, logSink.ContainsFieldValue("auth_latency_ms", "35"))
	require.True(t, logSink.ContainsFieldValue("routing_latency_ms", "75"))
	require.True(t, logSink.ContainsFieldValue("handler_to_semantic_ms", "12450"))

	logSink.mu.Lock()
	logged := fmt.Sprint(logSink.events)
	logSink.mu.Unlock()
	for _, secret := range []string{
		"sk-sensitive-api-key",
		"sensitive-cache-key",
		"sensitive prompt text",
		"sensitive tool output",
		"sensitive description",
	} {
		require.NotContains(t, logged, secret)
	}
}

func TestLogOpenAISlowTTFTDiagnosticSkipsFastRequests(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	logOpenAISlowTTFTDiagnostic(
		context.Background(),
		nil,
		&Account{ID: 1},
		[]byte(`{"model":"gpt-5.6-sol"}`),
		"gpt-5.6-sol",
		100,
		openAIStreamTiming{firstSemanticEventMs: 7999},
	)

	require.False(t, logSink.ContainsMessage("OpenAI passthrough slow first output"))
}

func slowTTFTInt64Ptr(value int64) *int64 {
	return &value
}

func TestSlowTTFTDiagnosticDoesNotExposeRequestBodyFields(t *testing.T) {
	fields := slowOpenAITTFTRequestFields(nil, []byte(`{"input":"secret"}`), "gpt-5.6-sol")
	for _, field := range fields {
		name := strings.ToLower(field.Key)
		require.NotContains(t, name, "body_preview")
		require.NotContains(t, name, "prompt_cache_key_value")
		require.NotContains(t, name, "authorization")
	}
}

func TestOpenAIForwardResultFirstTokenMsForSchedulingPrefersSemanticTiming(t *testing.T) {
	displayMs := 120
	semanticMs := 12_340
	result := &OpenAIForwardResult{
		FirstTokenMs:         &displayMs,
		SemanticFirstTokenMs: &semanticMs,
	}

	require.Same(t, result.SemanticFirstTokenMs, result.FirstTokenMsForScheduling())
	result.SemanticFirstTokenMs = nil
	require.Same(t, result.FirstTokenMs, result.FirstTokenMsForScheduling())
	var nilResult *OpenAIForwardResult
	require.Nil(t, nilResult.FirstTokenMsForScheduling())
}
