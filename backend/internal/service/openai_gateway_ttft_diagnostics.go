package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const openAISlowTTFTDiagnosticThresholdMs = 8_000

type openAIStreamTiming struct {
	forwardToResponseHeaderMs int
	firstUpstreamEventMs      int
	firstUpstreamEventType    string
	firstSemanticEventMs      int
	firstSemanticEventType    string
	upstreamRequestID         string
}

func logOpenAISlowTTFTDiagnostic(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	reqModel string,
	responseHeaderMs int64,
	timing openAIStreamTiming,
) {
	if timing.firstSemanticEventMs < openAISlowTTFTDiagnosticThresholdMs {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.String("request_model", strings.TrimSpace(reqModel)),
		zap.Int64("response_header_ms", responseHeaderMs),
		zap.Int("forward_to_response_header_ms", timing.forwardToResponseHeaderMs),
		zap.Int("first_semantic_event_ms", timing.firstSemanticEventMs),
		zap.String("first_semantic_event_type", timing.firstSemanticEventType),
		zap.String("upstream_request_id", strings.TrimSpace(timing.upstreamRequestID)),
	}
	if timing.firstUpstreamEventMs >= 0 {
		fields = append(fields, zap.Int("first_upstream_event_ms", timing.firstUpstreamEventMs))
		if timing.forwardToResponseHeaderMs >= 0 && timing.firstUpstreamEventMs >= timing.forwardToResponseHeaderMs {
			fields = append(fields, zap.Int("response_header_to_first_event_ms", timing.firstUpstreamEventMs-timing.forwardToResponseHeaderMs))
		}
	}
	if timing.firstUpstreamEventType != "" {
		fields = append(fields, zap.String("first_upstream_event_type", timing.firstUpstreamEventType))
	}
	if timing.firstUpstreamEventMs >= 0 && timing.firstSemanticEventMs >= timing.firstUpstreamEventMs {
		fields = append(fields, zap.Int("first_event_to_semantic_ms", timing.firstSemanticEventMs-timing.firstUpstreamEventMs))
	}
	fields = append(fields, slowOpenAITTFTRequestFields(c, body, reqModel)...)
	authLatencyMs, hasAuthLatency := openAIOpsLatencyMs(c, OpsAuthLatencyMsKey)
	routingLatencyMs, hasRoutingLatency := openAIOpsLatencyMs(c, OpsRoutingLatencyMsKey)
	if hasAuthLatency && hasRoutingLatency {
		fields = append(fields, zap.Int64("handler_to_semantic_ms", authLatencyMs+routingLatencyMs+int64(timing.firstSemanticEventMs)))
	}

	logger.FromContext(ctx).With(fields...).Warn("OpenAI passthrough slow first output")
}

func slowOpenAITTFTRequestFields(c *gin.Context, body []byte, reqModel string) []zap.Field {
	inputItems := gjson.GetBytes(body, "input")
	tools := gjson.GetBytes(body, "tools")
	promptCacheKeyPresent := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()) != ""
	previousResponseIDPresent := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String()) != ""
	reasoningEffort := ""
	if value := extractOpenAIReasoningEffortFromBody(body, reqModel); value != nil {
		reasoningEffort = strings.TrimSpace(*value)
	} else if value := strings.TrimSpace(gjson.GetBytes(body, "output_config.effort").String()); value != "" {
		reasoningEffort = value
	}

	fields := []zap.Field{
		zap.Int("request_body_bytes", len(body)),
		zap.Int("input_item_count", len(inputItems.Array())),
		zap.Int("tool_count", len(tools.Array())),
		zap.Bool("prompt_cache_key_present", promptCacheKeyPresent),
		zap.Bool("previous_response_id_present", previousResponseIDPresent),
		zap.String("reasoning_effort", reasoningEffort),
		zap.String("request_class", OpenAISchedulerPerformanceClass(body, reqModel)),
	}

	if c == nil {
		return fields
	}
	if c.Request != nil {
		fields = append(fields, zap.String("request_user_agent", strings.TrimSpace(c.Request.Header.Get("User-Agent"))))
	}
	authLatencyMs, hasAuthLatency := openAIOpsLatencyMs(c, OpsAuthLatencyMsKey)
	routingLatencyMs, hasRoutingLatency := openAIOpsLatencyMs(c, OpsRoutingLatencyMsKey)
	if hasAuthLatency {
		fields = append(fields, zap.Int64("auth_latency_ms", authLatencyMs))
	}
	if hasRoutingLatency {
		fields = append(fields, zap.Int64("routing_latency_ms", routingLatencyMs))
	}
	if apiKey := getAPIKeyFromContext(c); apiKey != nil {
		fields = append(fields,
			zap.Int64("user_id", apiKey.UserID),
			zap.Int64("api_key_id", apiKey.ID),
		)
		if apiKey.GroupID != nil {
			fields = append(fields, zap.Int64("group_id", *apiKey.GroupID))
		}
	}
	return fields
}

func openAIOpsLatencyMs(c *gin.Context, key string) (int64, bool) {
	if c == nil {
		return 0, false
	}
	value, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int64:
		return typed, typed >= 0
	case int:
		return int64(typed), typed >= 0
	default:
		return 0, false
	}
}

func openAIStreamDiagnosticEventType(eventType, data string) string {
	if value := strings.TrimSpace(eventType); value != "" {
		return value
	}
	if strings.TrimSpace(data) == "[DONE]" {
		return "[DONE]"
	}
	return "data"
}
