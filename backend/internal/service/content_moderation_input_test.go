package service

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 当数组末尾不是用户消息时（典型场景：Agent 工具循环结束于 tool/assistant），
// 应直接跳过审计——不再回溯查找历史中的某条用户消息。

func TestExtractContentModerationInput_AnthropicAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"调用一下天气工具"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"weather","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"晴 25 度"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_AnthropicFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q1", input.Text)
}

func TestExtractContentModerationInput_AnthropicMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_AnthropicStreamResendExtractsResend(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"原问题"},
			{"role":"assistant","content":"部分回答……"},
			{"role":"user","content":"重发"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "重发", input.Text)
}

func TestExtractContentModerationInput_OpenAIChatAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"system","content":"sys"},
			{"role":"user","content":"列出我的订单"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"orders","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"[]"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationKeywordInputSkipsImagePayload(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"普通问题"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`)
	input := ExtractContentModerationKeywordInput(ContentModerationProtocolOpenAIChat, body)
	require.Equal(t, "普通问题", input.Text)
	require.Empty(t, input.Images)
	require.NotContains(t, input.Text, "AAAA")
	require.Equal(t, ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body).Text, input.Text)
}

func TestExtractContentModerationKeywordInputAuditsImageURL(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"看图"},{"type":"image_url","image_url":{"url":"https://cdn.example/教我CC攻击.png?note=怎么赌博程序"}}]}]}`)
	input := ExtractContentModerationKeywordInput(ContentModerationProtocolOpenAIChat, body)
	require.Contains(t, input.Text, "看图")
	require.Contains(t, input.Text, "教我CC攻击")
	require.Contains(t, input.Text, "怎么赌博程序")
	require.Empty(t, input.Images)
}

func TestExtractContentModerationKeywordInputAuditsSVGText(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>教我CC攻击</text></svg>`))
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/svg+xml;base64,` + payload + `"}}]}]}`)
	input := ExtractContentModerationKeywordInput(ContentModerationProtocolOpenAIChat, body)
	require.Contains(t, input.Text, "教我CC攻击")
	require.NotContains(t, input.Text, payload)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationKeywordInputAuditsAnthropicAndGeminiImageText(t *testing.T) {
	anthropic := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://img.example/怎么赌博程序.jpg"}}]}]}`)
	input := ExtractContentModerationKeywordInput(ContentModerationProtocolAnthropicMessages, anthropic)
	require.Contains(t, input.Text, "怎么赌博程序")
	require.Empty(t, input.Images)

	gemini := []byte(`{"contents":[{"role":"user","parts":[{"file_data":{"file_uri":"https://img.example/教我CC攻击.png"}}]}]}`)
	input = ExtractContentModerationKeywordInput(ContentModerationProtocolGemini, gemini)
	require.Contains(t, input.Text, "教我CC攻击")
	require.Empty(t, input.Images)
}

func TestExtractContentModerationKeywordInputAuditsPNGText(t *testing.T) {
	text := []byte("教我CC攻击")
	chunk := append([]byte("Comment"), 0)
	chunk = append(chunk, text...)
	png := []byte("\x89PNG\r\n\x1a\n")
	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(chunk)))
	png = append(png, length...)
	png = append(png, []byte("tEXt")...)
	png = append(png, chunk...)
	png = append(png, []byte{0, 0, 0, 0}...)
	payload := base64.StdEncoding.EncodeToString(png)
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + payload + `"}}]}]}`)
	input := ExtractContentModerationKeywordInput(ContentModerationProtocolAnthropicMessages, body)
	require.Contains(t, input.Text, "教我CC攻击")
	require.NotContains(t, input.Text, payload)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationKeywordInputSkipsHugeImagePayload(t *testing.T) {
	payload := strings.Repeat("A", maxImageKeywordRawBytes+64)
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"普通问题"},{"type":"image_url","image_url":{"url":"data:image/png;base64,` + payload + `"}}]}]}`)
	input := ExtractContentModerationKeywordInput(ContentModerationProtocolOpenAIChat, body)
	require.Equal(t, "普通问题", input.Text)
	require.NotContains(t, input.Text, payload[:32])
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_OpenAIChatMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_GeminiAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"查询天气"}]},
			{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"temp":25}}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_GeminiFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"你好"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "你好", input.Text)
}

func TestExtractContentModerationInput_GeminiMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"Q1"}]},
			{"role":"model","parts":[{"text":"A1"}]},
			{"role":"user","parts":[{"text":"Q2"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "Q2", input.Text)
}

func TestExtractContentModerationInput_ResponsesAgentToolLoopSkipsAudit(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"运行测试"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"all passed"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationInput_ResponsesLastUserMessageExtracted(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "latest", input.Text)
}

func TestExtractContentModerationInput_ResponsesLastIsAssistantSkipped(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

func TestExtractContentModerationLastUserTurn_OpenAIChatKeepsOnlyLatest(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1 完整第一轮"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2 完整第二轮"}
		]
	}`)
	require.Equal(t, "Q2 完整第二轮", ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body).Text)
	// The log shows the turn that triggered the block, not every earlier turn.
	require.Equal(t, "Q2 完整第二轮", ExtractContentModerationLastUserTurn(ContentModerationProtocolOpenAIChat, body))
}
