package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEnsureOpenAICompactionTriggerLast_MovesTriggerToEnd(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"hi"},{"type":"compaction_trigger"},{"type":"additional_tools","role":"developer","tools":[]}]}`)
	fixed, changed, err := EnsureOpenAICompactionTriggerLast(body)
	require.NoError(t, err)
	require.True(t, changed)

	items := gjson.GetBytes(fixed, "input").Array()
	require.Len(t, items, 3)
	require.Equal(t, "compaction_trigger", items[len(items)-1].Get("type").String())
	require.Equal(t, "additional_tools", items[1].Get("type").String())
}

func TestEnsureOpenAICompactionTriggerLast_AlreadyLastUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"hi"},{"type":"compaction_trigger"}]}`)
	_, changed, err := EnsureOpenAICompactionTriggerLast(body)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestEnsureOpenAICompactionTriggerLast_NoTriggerUntouched(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"hi"}]}`)
	_, changed, err := EnsureOpenAICompactionTriggerLast(body)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestEnsureOpenAICompactionTriggerLast_DedupesMultipleTriggers(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"compaction_trigger"},{"type":"message","role":"user","content":"hi"},{"type":"compaction_trigger"}]}`)
	fixed, changed, err := EnsureOpenAICompactionTriggerLast(body)
	require.NoError(t, err)
	require.True(t, changed)

	items := gjson.GetBytes(fixed, "input").Array()
	require.Len(t, items, 2)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "compaction_trigger", items[1].Get("type").String())
}

func TestNormalizeOpenAIResponsesLiteTools_InsertsAdditionalToolsBeforeTrigger(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.5",
		"tools": []any{map[string]any{"type": "namespace", "name": "ns", "tools": []any{}}},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "hi"},
			map[string]any{"type": "compaction_trigger"},
		},
	}
	changed, err := normalizeOpenAIResponsesLiteTools(reqBody)
	require.NoError(t, err)
	require.True(t, changed)

	items, ok := reqBody["input"].([]any)
	require.True(t, ok)
	require.Len(t, items, 3)
	last, ok := items[len(items)-1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "compaction_trigger", last["type"])
	mid, ok := items[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "additional_tools", mid["type"])
}
