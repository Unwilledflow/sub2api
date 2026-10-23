package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 跨协议转换路径必须无条件补缓存断点：入站客户端说的是 OpenAI 协议，不可能携带
// Anthropic 专有的 cache_control。生产实测同一账号同一模型，原生 messages->messages
// 命中 99%，chat->messages 只有 0~3%，根因就是转换后 body 里没有任何断点。
func TestEnsureConvertedMessageCacheBreakpoints_InjectsWhenAbsent(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-5-5","system":[{"type":"text","text":"sys"}],"messages":[
		{"role":"user","content":[{"type":"text","text":"q1"}]},
		{"role":"assistant","content":[{"type":"text","text":"a1"}]},
		{"role":"user","content":[{"type":"text","text":"q2"}]},
		{"role":"assistant","content":[{"type":"text","text":"a2"}]}
	]}`)

	out := ensureConvertedMessageCacheBreakpoints(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.3.content.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(out, "messages.1.content.0.cache_control").Exists())
	require.False(t, gjson.GetBytes(out, "messages.2.content.0.cache_control").Exists())
	require.Equal(t, "q1", gjson.GetBytes(out, "messages.0.content.0.text").String())
	require.Equal(t, "a2", gjson.GetBytes(out, "messages.3.content.0.text").String())
}

func TestEnsureConvertedMessageCacheBreakpoints_Idempotent(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"q1"}]},
		{"role":"assistant","content":[{"type":"text","text":"a1"}]},
		{"role":"user","content":[{"type":"text","text":"q2"}]},
		{"role":"assistant","content":[{"type":"text","text":"a2"}]}
	]}`)

	once := ensureConvertedMessageCacheBreakpoints(body)
	twice := ensureConvertedMessageCacheBreakpoints(once)

	require.JSONEq(t, string(once), string(twice))
}

func TestEnsureConvertedMessageCacheBreakpoints_StripsClientAnchorInMiddle(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"q1","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"a1"}]},
		{"role":"user","content":[{"type":"text","text":"q2"}]}
	]}`)

	out := ensureConvertedMessageCacheBreakpoints(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.2.content.0.cache_control.type").String())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.2.content.0.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").Exists())
}

func TestEnsureConvertedMessageCacheBreakpoints_StringContentPromoted(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	out := ensureConvertedMessageCacheBreakpoints(body)

	require.True(t, gjson.GetBytes(out, "messages.0.content").IsArray())
	require.Equal(t, "hi", gjson.GetBytes(out, "messages.0.content.0.text").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
}

func TestEnsureConvertedMessageCacheBreakpoints_NoMessagesIsNoop(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-5-5","system":[{"type":"text","text":"sys"}]}`)
	out := ensureConvertedMessageCacheBreakpoints(body)
	require.JSONEq(t, string(body), string(out))
}

func TestEnsureConvertedMessageCacheBreakpoints_RespectsUpstreamBlockLimit(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"q1"}]},
		{"role":"assistant","content":[{"type":"text","text":"a1"}]},
		{"role":"user","content":[{"type":"text","text":"q2"}]},
		{"role":"assistant","content":[{"type":"text","text":"a2"}]}
	]}`)

	out := ensureConvertedMessageCacheBreakpoints(body)
	capped := enforceCacheControlLimit(out)

	count := 0
	gjson.GetBytes(capped, "messages").ForEach(func(_, msg gjson.Result) bool {
		msg.Get("content").ForEach(func(_, block gjson.Result) bool {
			if block.Get("cache_control").Exists() {
				count++
			}
			return true
		})
		return true
	})
	require.LessOrEqual(t, count, maxCacheControlBlocks)
	require.Equal(t, 2, count)
}