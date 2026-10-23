package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeriveOpenAIContentSessionSeed_EmptyInputs(t *testing.T) {
	require.Empty(t, deriveOpenAIContentSessionSeed(nil))
	require.Empty(t, deriveOpenAIContentSessionSeed([]byte{}))
	require.Empty(t, deriveOpenAIContentSessionSeed([]byte(`{}`)))
}

func TestDeriveOpenAIContentSessionSeed_ModelOnly(t *testing.T) {
	seed := deriveOpenAIContentSessionSeed([]byte(`{"model":"gpt-5.4"}`))
	require.Contains(t, seed, contentSessionSeedPrefix)
	require.Contains(t, seed, "model=gpt-5.4")
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_StableAcrossTurns(t *testing.T) {
	turn1 := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		]
	}`)
	turn2 := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "user", "content": "How are you?"}
		]
	}`)
	s1 := deriveOpenAIContentSessionSeed(turn1)
	s2 := deriveOpenAIContentSessionSeed(turn2)
	require.Equal(t, s1, s2, "seed should be stable across later turns")
	require.NotEmpty(t, s1)
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_IgnoresLaterSystemMessages(t *testing.T) {
	turn1 := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "user", "content": "How are you?"}
		]
	}`)
	turn2 := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there!"},
			{"role": "system", "content": "Return JSON for this turn."},
			{"role": "user", "content": "How are you?"}
		]
	}`)

	require.Equal(t, deriveOpenAIContentSessionSeed(turn1), deriveOpenAIContentSessionSeed(turn2))
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_UsesLeadingSystemDeveloperPrefix(t *testing.T) {
	firstSystem := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "System A"},
			{"role": "developer", "content": "Developer B"},
			{"role": "user", "content": "Hello"}
		]
	}`)
	changedLaterSystem := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "System A"},
			{"role": "developer", "content": "Developer C"},
			{"role": "user", "content": "Hello"}
		]
	}`)

	seed := deriveOpenAIContentSessionSeed(firstSystem)
	require.Contains(t, seed, "System A")
	require.Contains(t, seed, "Developer B")
	require.NotEqual(t, seed, deriveOpenAIContentSessionSeed(changedLaterSystem))

	withLaterSystem := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "System A"},
			{"role": "developer", "content": "Developer B"},
			{"role": "user", "content": "Hello"},
			{"role": "system", "content": "Dynamic system"}
		]
	}`)
	require.Equal(t, seed, deriveOpenAIContentSessionSeed(withLaterSystem))
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_DifferentFirstUserDiffers(t *testing.T) {
	req1 := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Question A"}]}`)
	req2 := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Question B"}]}`)
	s1 := deriveOpenAIContentSessionSeed(req1)
	s2 := deriveOpenAIContentSessionSeed(req2)
	require.NotEqual(t, s1, s2)
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_DifferentSystemDiffers(t *testing.T) {
	req1 := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"A"},{"role":"user","content":"Hi"}]}`)
	req2 := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"B"},{"role":"user","content":"Hi"}]}`)
	s1 := deriveOpenAIContentSessionSeed(req1)
	s2 := deriveOpenAIContentSessionSeed(req2)
	require.NotEqual(t, s1, s2)
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_DifferentModelDiffers(t *testing.T) {
	req1 := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}]}`)
	req2 := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`)
	s1 := deriveOpenAIContentSessionSeed(req1)
	s2 := deriveOpenAIContentSessionSeed(req2)
	require.NotEqual(t, s1, s2)
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_WithTools(t *testing.T) {
	withTools := []byte(`{
		"model": "gpt-5.4",
		"tools": [{"type":"function","function":{"name":"get_weather"}}],
		"messages": [{"role": "user", "content": "Hello"}]
	}`)
	withoutTools := []byte(`{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hello"}]
	}`)
	s1 := deriveOpenAIContentSessionSeed(withTools)
	s2 := deriveOpenAIContentSessionSeed(withoutTools)
	require.NotEqual(t, s1, s2, "tools should affect the seed")
	require.Contains(t, s1, "|tools=")
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_WithFunctions(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"functions": [{"name":"get_weather","parameters":{}}],
		"messages": [{"role": "user", "content": "Hello"}]
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|functions=")
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_DeveloperRole(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "developer", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		]
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	// developer 前缀按真实角色标记写入，不再统一折叠成 |system=，避免插件的
	// system/developer 两路独立种子塌陷到同一个粘性账号。
	require.Contains(t, seed, `|developer="You are helpful."`)
	require.Contains(t, seed, "|first_user=")
}

func TestDeriveOpenAIContentSessionSeed_ChatCompletions_StructuredContent(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": [{"type":"text","text":"Hello"}]}
		]
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.NotEmpty(t, seed)
	require.Contains(t, seed, "|first_user=")
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_InputString(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"Hello, how are you?"}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|input=Hello, how are you?")
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_InputArray(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		]
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|system=")
	require.Contains(t, seed, "|first_user=")
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_WithInstructions(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"instructions": "You are a coding assistant.",
		"input": "Write a hello world"
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|instructions=You are a coding assistant.")
	require.Contains(t, seed, "|input=Write a hello world")
}

func TestDeriveOpenAIContentSessionSeed_Deterministic(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		]
	}`)
	s1 := deriveOpenAIContentSessionSeed(body)
	s2 := deriveOpenAIContentSessionSeed(body)
	require.Equal(t, s1, s2, "seed must be deterministic")
}

func TestDeriveOpenAIContentSessionSeed_PrefixPresent(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}]}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.True(t, len(seed) > len(contentSessionSeedPrefix))
	require.Equal(t, contentSessionSeedPrefix, seed[:len(contentSessionSeedPrefix)])
}

func TestDeriveOpenAIContentSessionSeed_EmptyToolsIgnored(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","tools":[],"messages":[{"role":"user","content":"Hi"}]}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.NotContains(t, seed, "|tools=")
}

func TestDeriveOpenAIContentSessionSeed_MessagesPreferredOverInput(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "from messages"}],
		"input": "from input"
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|first_user=")
	require.NotContains(t, seed, "|input=")
}

func TestDeriveOpenAIContentSessionSeed_JSONCanonicalisation(t *testing.T) {
	compact := []byte(`{"model":"gpt-5.4","tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather"}}],"messages":[{"role":"user","content":"Hi"}]}`)
	spaced := []byte(`{
		"model": "gpt-5.4",
		"tools": [
			{ "type" : "function", "function": { "description": "Get weather", "name": "get_weather" } }
		],
		"messages": [ { "role": "user", "content": "Hi" } ]
	}`)
	s1 := deriveOpenAIContentSessionSeed(compact)
	s2 := deriveOpenAIContentSessionSeed(spaced)
	require.Equal(t, s1, s2, "different formatting of identical JSON should produce the same seed")
}

func TestDeriveOpenAIContentSessionSeed_SingleScanMatchesLegacyBytes(t *testing.T) {
	largeValue := strings.Repeat("payload", 1<<17)
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "large chat completions",
			body: []byte(`{"metadata":"` + largeValue + `","model":"gpt-5.4","tools":[{"type":"function","function":{"name":"lookup"}}],"functions":[{"name":"legacy_lookup"}],"messages":[{"role":"system","content":"System prompt"},{"role":"developer","content":[{"type":"text","text":"Developer prompt"}]},{"role":"user","content":"Hello"}]}`),
		},
		{
			name: "large responses",
			body: []byte(`{"metadata":"` + largeValue + `","model":"gpt-5.4","instructions":"Be concise.","tools":[{"type":"function","name":"lookup"}],"input":[{"role":"system","content":"System prompt"},{"role":"user","content":[{"type":"input_text","text":"Hello"}]}]}`),
		},
		{
			name: "fields in reverse order",
			body: []byte(`{"input":"fallback input","messages":[{"role":"user","content":"chat wins"}],"instructions":"Be concise.","functions":[{"name":"lookup"}],"tools":[{"type":"function","name":"lookup"}],"model":"gpt-5.4"}`),
		},
		{
			name: "missing and wrong type fields",
			body: []byte(`{"tools":[],"functions":null,"instructions":0,"messages":{},"input":[{"type":"input_text","text":"fallback"}]}`),
		},
		{
			name: "duplicate fields keep first value",
			body: []byte(`{"model":"first","model":"second","tools":[{"name":"first"}],"tools":[{"name":"second"}],"functions":[],"functions":[{"name":"second"}],"instructions":"first","instructions":"second","messages":null,"messages":[{"role":"user","content":"second"}],"input":"first input","input":"second input"}`),
		},
		{
			name: "escaped field names",
			body: []byte(`{"mo\u0064el":"gpt-5.4","mess\u0061ges":[{"role":"user","content":"Hello"}]}`),
		},
		{
			name: "trailing object fields are outside the root",
			body: []byte(`{"foo":1}{"model":"trailing","input":"trailing input"}`),
		},
		{
			name: "trailing quoted fields are outside the root",
			body: []byte(`{"model":"root"}"input":"trailing input"`),
		},
		{
			name: "leading garbage before the root",
			body: []byte(`garbage{"model":"gpt-5.4","input":"Hello"}`),
		},
		{
			name: "escaped braces remain inside string values",
			body: []byte(`{"metadata":"escaped } and [ and \" quote","model":"root","input":"Hello"}{"model":"trailing"}`),
		},
		{
			name: "nested braces do not end the root",
			body: []byte(`{"metadata":{"nested":"} ]"},"model":"root","input":"Hello"}{"model":"trailing"}`),
		},
		{
			name: "root array does not expose nested or trailing object fields",
			body: []byte(`[{"model":"nested"}]{"model":"trailing","input":"trailing input"}`),
		},
		{
			name: "trailing messages do not override root input",
			body: []byte(`{"input":"root input"}{"messages":[{"role":"user","content":"trailing"}]}`),
		},
		{
			name: "truncated string containing a closing brace",
			body: []byte(`{"model":"root","metadata":"still } inside`),
		},
		{
			name: "lenient truncated body",
			body: []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Hello"}]`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, referenceDeriveOpenAIContentSessionSeed(test.body), deriveOpenAIContentSessionSeed(test.body))
		})
	}
}

func TestDeriveOpenAIContentSessionSeed_AllTruncationOffsetsMatchLegacyBytes(t *testing.T) {
	bodies := []string{
		`{"model":"gpt-5.4","tools":[{"type":"function","function":{"name":"lookup"}}],"functions":[{"name":"legacy"}],"instructions":"escaped \" } text","messages":[{"role":"system","content":"System"},{"role":"user","content":[{"type":"text","text":"Hello"}]}],"input":"fallback"}`,
		`{"model":"gpt-5.4","instructions":"Be concise.","tools":[{"type":"function","name":"lookup"}],"input":[{"role":"system","content":"System"},{"role":"user","content":[{"type":"input_text","text":"Hello"}]}]}`,
	}
	for bodyIndex, body := range bodies {
		for end := 1; end < len(body); end++ {
			truncated := []byte(body[:end])
			require.Equalf(t, referenceDeriveOpenAIContentSessionSeed(truncated), deriveOpenAIContentSessionSeed(truncated), "body %d truncated at byte %d", bodyIndex, end)
		}
	}
}

func referenceDeriveOpenAIContentSessionSeed(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var b strings.Builder

	if model := gjson.GetBytes(body, "model").String(); model != "" {
		_, _ = b.WriteString("model=")
		_, _ = b.WriteString(model)
	}

	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && tools.IsArray() && tools.Raw != "[]" {
		_, _ = b.WriteString("|tools=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(tools.Raw)))
	}

	if funcs := gjson.GetBytes(body, "functions"); funcs.Exists() && funcs.IsArray() && funcs.Raw != "[]" {
		_, _ = b.WriteString("|functions=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(funcs.Raw)))
	}

	if instr := gjson.GetBytes(body, "instructions").String(); instr != "" {
		_, _ = b.WriteString("|instructions=")
		_, _ = b.WriteString(instr)
	}

	firstUserCaptured := false
	roleEmitted := make(map[string]struct{}, 2)

	msgs := gjson.GetBytes(body, "messages")
	if msgs.Exists() && msgs.IsArray() {
		for _, msg := range msgs.Array() {
			switch role := msg.Get("role").String(); role {
			case "system", "developer":
				referenceAppendContentSeedPrefix(&b, roleEmitted, role, msg)
			case "user":
				if !firstUserCaptured {
					if anchor := referenceStableUserTurnAnchor(msg.Get("content")); anchor != "" {
						_, _ = b.WriteString("|first_user=")
						_, _ = b.WriteString(anchor)
					}
					firstUserCaptured = true
				}
			}
		}
	} else if inp := gjson.GetBytes(body, "input"); inp.Exists() {
		if inp.Type == gjson.String {
			_, _ = b.WriteString("|input=")
			_, _ = b.WriteString(inp.String())
		} else if inp.IsArray() {
			for _, item := range inp.Array() {
				switch role := item.Get("role").String(); role {
				case "system", "developer":
					referenceAppendContentSeedPrefix(&b, roleEmitted, role, item)
				case "user":
					if !firstUserCaptured {
						if anchor := referenceStableUserTurnAnchor(item.Get("content")); anchor != "" {
							_, _ = b.WriteString("|first_user=")
							_, _ = b.WriteString(anchor)
						}
						firstUserCaptured = true
					}
				}
				if !firstUserCaptured && item.Get("type").String() == "input_text" {
					if anchor := referenceStableUserTurnAnchor(item.Get("text")); anchor != "" {
						_, _ = b.WriteString("|first_user=")
						_, _ = b.WriteString(anchor)
					}
					firstUserCaptured = true
				}
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return contentSessionSeedPrefix + b.String()
}

func referenceStableUserTurnAnchor(content gjson.Result) string {
	hashes := referenceReusableTurnContentHashes(content)
	if len(hashes) == 0 {
		return ""
	}
	start := len(hashes) - contentSeedReusableTurnHashes
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for _, h := range hashes[start:] {
		b.WriteString(h)
		b.WriteString(",")
	}
	return b.String()
}

// referenceAppendContentSeedPrefix 是 appendContentSeedPrefix 的直译版本。
func referenceAppendContentSeedPrefix(b *strings.Builder, roleEmitted map[string]struct{}, role string, item gjson.Result) {
	if _, done := roleEmitted[role]; done {
		return
	}
	content := item.Get("content")
	if !content.Exists() || content.Type == gjson.Null {
		return
	}
	if content.Type != gjson.String && !content.IsArray() && !content.IsObject() {
		return
	}
	normalized := normalizeCompatSeedJSON(json.RawMessage(content.Raw))
	if strings.TrimSpace(normalized) == "" {
		return
	}
	roleEmitted[role] = struct{}{}
	_, _ = b.WriteString("|" + role + "=")
	_, _ = b.WriteString(normalized)
}

// referenceReusableTurnContentHashes 是 reusableTurnContentHashes 的直译版本，
// 用 gjson.Get + ForEach 而非 Result.ForEach，用来交叉校验解析路径。
func referenceReusableTurnContentHashes(content gjson.Result) []string {
	if !content.Exists() || content.Type == gjson.Null {
		return nil
	}
	if content.Type == gjson.String || !content.IsArray() {
		return []string{fmt.Sprintf("%016x", xxhash.Sum64String(content.String()))}
	}
	hashes := make([]string, 0, 4)
	for _, item := range content.Array() {
		switch {
		case item.Type == gjson.String:
			hashes = append(hashes, fmt.Sprintf("%016x", xxhash.Sum64String(item.String())))
		case item.Get("text").Exists() && item.Get("text").Type == gjson.String:
			hashes = append(hashes, fmt.Sprintf("%016x", xxhash.Sum64String(item.Get("text").String())))
		}
	}
	return hashes
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_InputTextTypedItem(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"input": [{"type": "input_text", "text": "Hello world"}]
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|first_user=")
	require.Contains(t, seed, contentSeedTurnHash("Hello world"))
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_TypedMessageItem(t *testing.T) {
	body := []byte(`{
		"model": "gpt-5.4",
		"input": [{"type": "message", "role": "user", "content": "Hello from typed message"}]
	}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, "|first_user=")
	require.Contains(t, seed, contentSeedTurnHash("Hello from typed message"))
}

// DSH 这类客户端把易变的运行时上下文（时间戳、应用动态上下文）放在每轮的
// 第一条 developer 消息里，并把当前请求原样包成一条新的 input_text。旧实现把两者
// 都写进种子，导致每轮种子都变、每轮重新负载均衡（生产实测 65% 相邻请求换账号）。
// 新实现：developer 前缀每个角色只取第一次出现 + 用户输入取最近复用周期。
func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_VolatileFirstItemStaysStable(t *testing.T) {
	turn := func(ts, step, firstText string) []byte {
		return []byte(`{
			"model": "deepseek-v4.1-flash",
			"input": [
				{"type": "message", "role": "developer", "content": "runtime context ` + ts + ` ` + step + `"},
				{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "这段调度是不是有问题"}]},
				{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "结论：` + step + `"}]},
				{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "` + firstText + `"}]}
			]
		}`)
	}
	first := deriveOpenAIContentSessionSeed(turn("2026-10-22T21:06:30Z", "step-1", "帮我修复"))
	second := deriveOpenAIContentSessionSeed(turn("2026-10-22T21:07:02Z", "step-2", "继续"))
	third := deriveOpenAIContentSessionSeed(turn("2026-10-22T21:08:44Z", "step-3", "再说一遍"))
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
	require.Equal(t, second, third)
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_SecondDeveloperTurnDoesNotShift(t *testing.T) {
	// 开局请求（无历史轮次）里首个 developer 是固定前缀，允许进种子；
	// 有历史之后每轮追加的 developer 注解（runtime B）不得进入种子，
	// 否则续轮之间必然换种子。
	opening := []byte(`{
		"model": "deepseek-v4.1-flash",
		"input": [
			{"type": "message", "role": "developer", "content": "runtime A"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "继续"}]}
		]
	}`)
	openingSeed := deriveOpenAIContentSessionSeed(opening)
	require.Contains(t, openingSeed, `|developer="runtime A"`)

	resumed := []byte(`{
		"model": "deepseek-v4.1-flash",
		"input": [
			{"type": "message", "role": "developer", "content": "runtime A"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "继续"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "好的"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "再说一遍"}]}
		]
	}`)
	resumedWithExtraDeveloper := []byte(`{
		"model": "deepseek-v4.1-flash",
		"input": [
			{"type": "message", "role": "developer", "content": "runtime A"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "继续"}]},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "好的"}]},
			{"type": "message", "role": "developer", "content": "runtime B different"},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "再说一遍"}]}
		]
	}`)
	seed := deriveOpenAIContentSessionSeed(resumed)
	require.Equal(t, seed, deriveOpenAIContentSessionSeed(resumedWithExtraDeveloper))
	require.NotContains(t, seed, "runtime A")
	require.NotContains(t, seed, "runtime B")
}

func TestDeriveOpenAIContentSessionSeed_ResponsesAPI_IndependentPromptsStayIsolated(t *testing.T) {
	a := deriveOpenAIContentSessionSeed([]byte(`{"model":"gpt-5.4","input":[{"type":"input_text","text":"任务 A"}]}`))
	b := deriveOpenAIContentSessionSeed([]byte(`{"model":"gpt-5.4","input":[{"type":"input_text","text":"任务 B"}]}`))
	require.NotEqual(t, a, b)
}

func TestDeriveOpenAIContentSessionSeed_KeepsSingleSystemPrefixSemantics(t *testing.T) {
	// Chat Completions 只取 model 之后的第一个 system/developer；两轮前导前缀一致时
	// 种子必须完全一致（与旧实现同语义）。
	turn := func(user string) []byte {
		return []byte(`{
			"model": "gpt-5.4",
			"messages": [
				{"role": "system", "content": "You are helpful."},
				{"role": "developer", "content": "Be concise."},
				{"role": "user", "content": "` + user + `"}
			]
		}`)
	}
	seed := deriveOpenAIContentSessionSeed(turn("Hello"))
	require.Equal(t, seed, deriveOpenAIContentSessionSeed(turn("Hello")))
	require.Contains(t, seed, `|system="You are helpful."`)
	require.Contains(t, seed, `|developer="Be concise."`)
}

func TestDeriveOpenAIContentSessionSeed_UserAnchorWindowIsBounded(t *testing.T) {
	content := make([]string, 0, contentSeedReusableTurnHashes+4)
	for i := 0; i < contentSeedReusableTurnHashes+4; i++ {
		content = append(content, `{"type":"input_text","text":"turn-`+strconv.Itoa(i)+`"}`)
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[` + strings.Join(content, ",") + `]}]}`)
	seed := deriveOpenAIContentSessionSeed(body)
	require.Contains(t, seed, contentSeedTurnHash("turn-"+(strconv.Itoa(contentSeedReusableTurnHashes+3))))
	require.NotContains(t, seed, contentSeedTurnHash("turn-0"))
}

func TestReusableTurnContentHashes_Shape(t *testing.T) {
	require.Empty(t, reusableTurnContentHashes(gjson.Result{}))
	require.Empty(t, reusableTurnContentHashes(gjson.Parse(`""`)))
	require.Equal(t, []string{contentSeedTurnHash("plain")}, reusableTurnContentHashes(gjson.Parse(`"plain"`)))
	// 只有 output_text（助手输出）不构成用户锚点。
	require.Empty(t, reusableTurnContentHashes(gjson.Parse(`[{"type":"output_text","text":"assistant only"}]`)))
	require.Equal(t,
		[]string{contentSeedTurnHash("one"), contentSeedTurnHash("two")},
		reusableTurnContentHashes(gjson.Parse(`[{"type":"input_text","text":"one"},"two"]`)))
}

func TestSplitResponsesContentSeedItems_KeepsUserOnlyRequestsStable(t *testing.T) {
	leading, history, turns := splitResponsesContentSeedItems(gjson.Parse(`[{"role":"user","content":"Hello"}]`).Array())
	require.Empty(t, leading)
	require.Empty(t, history)
	require.Len(t, turns, 1)

	leading, history, turns = splitResponsesContentSeedItems(gjson.Parse(`[
		{"role":"developer","content":"runtime"},
		{"role":"user","content":"A"},
		{"role":"assistant","content":"B"},
		{"role":"user","content":"C"}
	]`).Array())
	require.Len(t, leading, 1, "前导含固定 system/developer 前缀")
	require.Len(t, history, 2)
	require.Len(t, turns, 1)

	// 无消息角色的顶层 input_text 归入前导，最后一段连续 user 归入本轮。
	leading, history, turns = splitResponsesContentSeedItems(gjson.Parse(`[
		{"type":"input_text","text":"current"},
		{"role":"user","content":"A"},
		{"role":"user","content":"B"}
	]`).Array())
	require.Len(t, leading, 1)
	require.Empty(t, history)
	require.Len(t, turns, 2)
}
func contentSeedTurnHash(text string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(text))
}

func TestDeriveOpenAIStablePrefixSessionSeed_IgnoresUserContent(t *testing.T) {
	first := []byte(`{
		"model": "grok",
		"instructions": "Be concise.",
		"tools": [{"type":"function","name":"lookup","parameters":{"type":"object"}}],
		"input": [{"role":"user","content":"Question A"}]
	}`)
	second := []byte(`{
		"model": "grok",
		"instructions": "Be concise.",
		"tools": [{"parameters":{"type":"object"},"name":"lookup","type":"function"}],
		"input": [{"role":"user","content":"Question B"}]
	}`)

	firstSeed := deriveOpenAIStablePrefixSessionSeed(first)
	secondSeed := deriveOpenAIStablePrefixSessionSeed(second)

	require.NotEmpty(t, firstSeed)
	require.Equal(t, firstSeed, secondSeed)
	require.NotContains(t, firstSeed, "Question A")
	require.NotContains(t, firstSeed, "first_user")
}

func TestDeriveOpenAIStablePrefixSessionSeed_IsolatesStablePrefixFields(t *testing.T) {
	base := []byte(`{
		"instructions":"Be concise.",
		"tools":[{"type":"function","name":"lookup"}],
		"input":[{"role":"system","content":"System A"},{"role":"user","content":"Question"}]
	}`)
	differentInstructions := []byte(`{
		"instructions":"Be detailed.",
		"tools":[{"type":"function","name":"lookup"}],
		"input":[{"role":"system","content":"System A"},{"role":"user","content":"Question"}]
	}`)
	differentTools := []byte(`{
		"instructions":"Be concise.",
		"tools":[{"type":"function","name":"search"}],
		"input":[{"role":"system","content":"System A"},{"role":"user","content":"Question"}]
	}`)
	differentSystem := []byte(`{
		"instructions":"Be concise.",
		"tools":[{"type":"function","name":"lookup"}],
		"input":[{"role":"system","content":"System B"},{"role":"user","content":"Question"}]
	}`)

	baseSeed := deriveOpenAIStablePrefixSessionSeed(base)
	require.NotEqual(t, baseSeed, deriveOpenAIStablePrefixSessionSeed(differentInstructions))
	require.NotEqual(t, baseSeed, deriveOpenAIStablePrefixSessionSeed(differentTools))
	require.NotEqual(t, baseSeed, deriveOpenAIStablePrefixSessionSeed(differentSystem))
}

func TestDeriveOpenAIStablePrefixSessionSeed_ChatSystemAndDeveloper(t *testing.T) {
	first := []byte(`{
		"messages":[
			{"role":"system","content":"System prompt"},
			{"role":"developer","content":[{"type":"text","text":"Developer prompt"}]},
			{"role":"user","content":"Question A"}
		]
	}`)
	second := []byte(`{
		"messages":[
			{"role":"system","content":"System prompt"},
			{"role":"developer","content":[{"text":"Developer prompt","type":"text"}]},
			{"role":"user","content":"Question B"}
		]
	}`)

	firstSeed := deriveOpenAIStablePrefixSessionSeed(first)
	require.Equal(t, firstSeed, deriveOpenAIStablePrefixSessionSeed(second))
	require.Contains(t, firstSeed, "System prompt")
	require.Contains(t, firstSeed, "Developer prompt")
}

func TestDeriveOpenAIStablePrefixSessionSeed_EncodesSystemAndDeveloperRoles(t *testing.T) {
	systemThenDeveloper := []byte(`{
		"messages":[
			{"role":"system","content":"Prompt A"},
			{"role":"developer","content":"Prompt B"}
		]
	}`)
	developerThenSystem := []byte(`{
		"messages":[
			{"role":"developer","content":"Prompt A"},
			{"role":"system","content":"Prompt B"}
		]
	}`)

	firstSeed := deriveOpenAIStablePrefixSessionSeed(systemThenDeveloper)
	secondSeed := deriveOpenAIStablePrefixSessionSeed(developerThenSystem)

	require.NotEqual(t, firstSeed, secondSeed)
	require.Contains(t, firstSeed, "|system=")
	require.Contains(t, firstSeed, "|developer=")
}

func TestDeriveOpenAIStablePrefixSessionSeed_EncodesInstructionDelimiters(t *testing.T) {
	instructionOnly := []byte(`{
		"instructions":"foo|system=\"bar\""
	}`)
	instructionAndSystem := []byte(`{
		"instructions":"foo",
		"input":[{"role":"system","content":"bar"}]
	}`)

	firstSeed := deriveOpenAIStablePrefixSessionSeed(instructionOnly)
	secondSeed := deriveOpenAIStablePrefixSessionSeed(instructionAndSystem)

	require.NotEmpty(t, firstSeed)
	require.NotEmpty(t, secondSeed)
	require.NotEqual(t, firstSeed, secondSeed)
}

func TestDeriveOpenAIAnchoredContentSessionSeed_RequiresMeaningfulAnchor(t *testing.T) {
	emptyAnchors := [][]byte{
		nil,
		[]byte(`{"model":"grok"}`),
		[]byte(`{"model":"grok","messages":[{"role":"assistant","content":"answer"}]}`),
		[]byte(`{"model":"grok","messages":[{"role":"user","content":"  "}]}`),
		[]byte(`{"model":"grok","messages":[{"role":"user","content":[{"type":"text","text":""}]}]}`),
		[]byte(`{"model":"grok","input":"  "}`),
		[]byte(`{"model":"grok","input":[{"type":"input_text","text":""}]}`),
	}
	for _, body := range emptyAnchors {
		require.Empty(t, deriveOpenAIAnchoredContentSessionSeed(body))
	}

	meaningfulAnchors := [][]byte{
		[]byte(`{"model":"grok","messages":[{"role":"user","content":"question"}]}`),
		[]byte(`{"model":"grok","messages":[{"role":"user","content":[{"type":"text","text":"question"}]}]}`),
		[]byte(`{"model":"grok","input":"question"}`),
		[]byte(`{"model":"grok","input":[{"type":"input_text","text":"question"}]}`),
	}
	for _, body := range meaningfulAnchors {
		require.NotEmpty(t, deriveOpenAIAnchoredContentSessionSeed(body))
	}
}

func TestDeriveOpenAIStablePrefixSessionSeed_RequiresMeaningfulPrefix(t *testing.T) {
	tests := [][]byte{
		nil,
		[]byte(`{}`),
		[]byte(`{"model":"grok","input":"Question A"}`),
		[]byte(`{"model":"grok","tools":[],"input":"Question A"}`),
		[]byte(`{"model":"grok","functions":[],"instructions":"  ","messages":[{"role":"system","content":""},{"role":"user","content":"Question A"}]}`),
	}

	for _, body := range tests {
		require.Empty(t, deriveOpenAIStablePrefixSessionSeed(body))
	}
}
