package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func thinkStrPtr(s string) *string { return &s }

func TestThinkingStripper_RemovesCompleteBlocks(t *testing.T) {
	var s ThinkingStripper
	got := s.Process("<thinking>plan</thinking>answer <think>more</think>end")
	got += s.Flush()
	assert.Equal(t, "answer end", got)
}

func TestThinkingStripper_TagsSplitAcrossChunks(t *testing.T) {
	// " text" 位于 <thinking> 块内部，属于推理内容，应一并剥离。
	var s ThinkingStripper
	var out string
	for _, chunk := range []string{"<thi", "nking>hidden text</think", "ing>visible"} {
		out += s.Process(chunk)
	}
	out += s.Flush()
	assert.Equal(t, "visible", out)
}

func TestThinkingStripper_PreservesPlainText(t *testing.T) {
	var s ThinkingStripper
	got := s.Process("hello < world") + s.Flush()
	assert.Equal(t, "hello < world", got)
}

func TestThinkingStripper_UnterminatedBlockDiscarded(t *testing.T) {
	var s ThinkingStripper
	got := s.Process("a<thinking>unterminated") + s.Flush()
	assert.Equal(t, "a", got)
}

func TestStripThinkingBlocks(t *testing.T) {
	assert.Equal(t, "ab", StripThinkingBlocks("a<thinking>x<think>y</think>z</thinking>b"))
	assert.Equal(t, "a", StripThinkingBlocks("a<thinking>unterminated"))
	assert.Equal(t, "plain", StripThinkingBlocks("plain"))
}

func TestChatCompletionsChunkToResponsesEvents_StripsThinkingFromContent(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-5.5")
	chunk := &ChatCompletionsChunk{
		Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: thinkStrPtr("<thinking>plan</thinking>final answer")}}},
	}
	events := ChatCompletionsChunkToResponsesEvents(chunk, state)
	events = append(events, FinalizeChatCompletionsResponsesStream(state)...)

	var deltas, doneText string
	for _, e := range events {
		switch e.Type {
		case "response.output_text.delta":
			deltas += e.Delta
		case "response.output_text.done":
			doneText = e.Text
		}
	}
	assert.Equal(t, "final answer", deltas)
	assert.Equal(t, "final answer", doneText)
	assert.NotContains(t, deltas, "<thinking>")
}

func TestChatCompletionsToResponses_StripsThinkingFromOutputText(t *testing.T) {
	resp := ChatCompletionsResponseToResponses(&ChatCompletionsResponse{
		Choices: []ChatChoice{{Message: ChatMessage{
			Content: json.RawMessage(`"<thinking>plan</thinking>final answer"`),
		}}},
	}, "gpt-5.5", nil, nil, false, nil)

	require.Len(t, resp.Output, 1)
	require.Equal(t, "message", resp.Output[0].Type)
	require.Len(t, resp.Output[0].Content, 1)
	assert.Equal(t, "final answer", resp.Output[0].Content[0].Text)
}

func TestChatCompletionsChunkToAnthropicEvents_StripsThinkingFromText(t *testing.T) {
	state := NewChatCompletionsToAnthropicStreamState("gpt-5.5")
	chunk := &ChatCompletionsChunk{
		Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: thinkStrPtr("<thinking>plan</thinking>final")}}},
	}
	events := ChatCompletionsChunkToAnthropicEvents(chunk, state)
	events = append(events, FinalizeChatCompletionsAnthropicStream(state)...)

	var text string
	for _, e := range events {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "text_delta" {
			text += e.Delta.Text
		}
	}
	assert.Equal(t, "final", text)
}
