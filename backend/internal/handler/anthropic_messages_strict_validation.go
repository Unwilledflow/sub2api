package handler

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// ValidateStrictOpusMessagesRequest 校验 claude-opus-5-5 的 /v1/messages 请求体。
// 非法参数直接 400，不在转发前修好。必须在补 max_tokens 或 temperature 之前调用。
func ValidateStrictOpusMessagesRequest(model string, body []byte) (string, bool) {
	if !claude.IsOpus55(model) {
		return "", true
	}
	if len(body) == 0 {
		return "field messages is required", false
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return "field messages is required", false
	}

	maxTokens := gjson.GetBytes(body, "max_tokens")
	if !maxTokens.Exists() || maxTokens.Type != gjson.Number || maxTokens.Float() < 1 {
		return "max_tokens: Field required", false
	}

	if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() {
		if temperature.Type != gjson.Number || temperature.Float() < 0 || temperature.Float() > 1 {
			return "temperature: range: 0..1", false
		}
	}

	for i, message := range messages.Array() {
		content := message.Get("content")
		if !content.IsArray() {
			continue
		}
		for j, block := range content.Array() {
			if block.Get("type").String() != "text" {
				continue
			}
			if strings.TrimSpace(block.Get("text").String()) == "" {
				return fmt.Sprintf(
					"messages.%d.content.%d.text: text content blocks must contain non-whitespace text", i, j), false
			}
		}
	}

	if thinkingType := strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()); thinkingType == "enabled" || thinkingType == "adaptive" {
		if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() && temperature.Float() != 1 {
			return "temperature may only be set to 1 when thinking is enabled", false
		}
		if topP := gjson.GetBytes(body, "top_p"); topP.Exists() && topP.Float() != 1 {
			return "top_p may only be set to 1 when thinking is enabled", false
		}
		if topK := gjson.GetBytes(body, "top_k"); topK.Exists() && topK.Float() != 0 {
			return "top_k may only be set to 0 when thinking is enabled", false
		}
	}

	return "", true
}
