package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/tidwall/gjson"
)

// contentSessionSeedPrefix prevents collisions between content-derived seeds
// and explicit session IDs (e.g. "sess-xxx" or "compat_cc_xxx").
const contentSessionSeedPrefix = "compat_cs_"

// contentStablePrefixSessionSeedPrefix distinguishes cache identities derived
// only from request fields that remain stable across independent prompts.
const contentStablePrefixSessionSeedPrefix = "compat_csp_"

// deriveOpenAIContentSessionSeed builds a stable session seed from an
// OpenAI-format request body. Only fields constant across conversation turns
// are included: model, tools/functions definitions, the system/developer prompt
// prefix in Chat messages, instructions (Responses API), and the first user
// message.
// Supports both Chat Completions (messages) and Responses API (input).
//
// 追加的「最近复用周期」是无会话头链路的关键：此前只取“第一条 user 消息 /
// 第一个 input_text”，而 DSH 这类客户端每轮第一条 input 都是易变的运行时上下文
// （时间戳、动态注入），文件名还随每步工具调用变化 → 每轮种子不同 → 每轮重新
// 负载均衡 → 上游前缀缓存整段未命中。改为对全部 user/input_text 做 xxhash 后
// 取最近 (非最旧) 的 8 个 raw，输入只含此前任意轮次 id 时即可复现同一周期，从而
// 跨轮稳定；工具调用产生的变动 content 只影响同一请求内，不影响周期身份。
func deriveOpenAIContentSessionSeed(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	const (
		modelField = iota
		toolsField
		functionsField
		instructionsField
		messagesField
		inputField
		contentSessionSeedFieldCount
		allContentSessionSeedFields = 1<<contentSessionSeedFieldCount - 1
	)
	var fields [contentSessionSeedFieldCount]gjson.Result
	var seen uint8
	// Match gjson.GetBytes by starting at the first root container, even when
	// malformed input has a non-JSON prefix.
	root := body
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			root = body[i:]
			goto scanRoot
		case '[':
			return ""
		}
	}
	return ""

scanRoot:
	nextKeyOffset := 1
	parseRawJSONView(root).ForEach(func(key, value gjson.Result) bool {
		if key.Index < nextKeyOffset || key.Index > len(root) {
			return false
		}
		// Result.ForEach can continue after the root '}' on malformed input.
		// The separator range excludes braces inside the preceding parsed value.
		if bytes.IndexByte(root[nextKeyOffset:key.Index], '}') >= 0 {
			return false
		}
		nextKeyOffset = value.Index + len(value.Raw)

		field := -1
		switch key.Str {
		case "model":
			field = modelField
		case "tools":
			field = toolsField
		case "functions":
			field = functionsField
		case "instructions":
			field = instructionsField
		case "messages":
			field = messagesField
		case "input":
			field = inputField
		}
		if field < 0 {
			return true
		}
		mask := uint8(1 << field)
		if seen&mask == 0 {
			fields[field] = value
			seen |= mask
		}
		return seen != allContentSessionSeedFields
	})

	var b strings.Builder

	if model := fields[modelField].String(); model != "" {
		_, _ = b.WriteString("model=")
		_, _ = b.WriteString(model)
	}

	if tools := fields[toolsField]; tools.Exists() && tools.IsArray() && tools.Raw != "[]" {
		_, _ = b.WriteString("|tools=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(tools.Raw)))
	}

	if funcs := fields[functionsField]; funcs.Exists() && funcs.IsArray() && funcs.Raw != "[]" {
		_, _ = b.WriteString("|functions=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(funcs.Raw)))
	}

	if instr := fields[instructionsField].String(); instr != "" {
		_, _ = b.WriteString("|instructions=")
		_, _ = b.WriteString(instr)
	}

	// 每个请求的 role 前缀只允许出现一次：Responses 的 input 每轮都会追加同类消息，
	// 重复写入会让前缀身份漂移。
	roleEmitted := make(map[string]struct{}, 2)

	msgs := fields[messagesField]
	if msgs.Exists() && msgs.IsArray() {
		// Chat Completions：保留旧语义，只看前导 system/developer 前缀。同角色只取
		// 第一次出现，用户输入取最近复用周期。
		firstUserCaptured := false
		msgs.ForEach(func(_, msg gjson.Result) bool {
			switch role := msg.Get("role").String(); role {
			case "system", "developer":
				appendContentSeedPrefix(&b, roleEmitted, role, msg)
			case "user":
				captureResponsesUserAnchor(&b, &firstUserCaptured, msg.Get("content"))
			}
			return true
		})
	} else if inp := fields[inputField]; inp.Exists() {
		if inp.Type == gjson.String {
			_, _ = b.WriteString("|input=")
			_, _ = b.WriteString(inp.String())
		} else if inp.IsArray() {
			leading, history, turns := splitResponsesContentSeedItems(inp.Array())
			// 前缀槽位只在「没有历史轮次」的开局请求里写。有历史时，客户端（如 DSH）
			// 每轮都会把一条易变的运行时上下文 developer 放在首位；写进种子就等于
			// 每轮换一个会话身份 → 每轮重新负载均衡（生产实测 65% 相邻请求换账号）。
			// 开局请求里的首个 system/developer 才是真正固定的前缀。
			if len(history) == 0 {
				for _, item := range leading {
					switch role := item.Get("role").String(); role {
					case "system", "developer":
						appendContentSeedPrefix(&b, roleEmitted, role, item)
					}
				}
			}
			firstUserCaptured := false
			for _, item := range leading {
				captureResponsesSeedAnchor(&b, &firstUserCaptured, item)
			}
			for _, item := range history {
				captureResponsesSeedAnchor(&b, &firstUserCaptured, item)
			}
			for _, item := range turns {
				captureResponsesSeedAnchor(&b, &firstUserCaptured, item)
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return contentSessionSeedPrefix + b.String()
}

// contentSeedReusableTurnHashes 是参与种子计算的“最近复用周期”数量：固定 N 后
// 同一对话的第 t 轮与第 t+1 轮只要都包含其中任意一轮的用户输入，种子即相同。
const contentSeedReusableTurnHashes = 8

// appendContentSeedPrefix 写入<角色>="内容"前缀对：每个角色最多一次，避免每轮追加
// 的同类消息让前缀身份漂移。
func appendContentSeedPrefix(b *strings.Builder, roleEmitted map[string]struct{}, role string, item gjson.Result) {
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

// stableUserTurnAnchor 返回随轮次稳定、对 DSH 这类客户端不漂移的会话锚点：
// 全部用户输入 raw 的哈希，按出现顺序取最近 contentSeedReusableTurnHashes 个。
// 只看 raw 哈希、不裁剪，请求只含此前轮次输入时结果与旧实现一致；没有任何用户
// 输入时返回空串，由 model/system/tools 兜底，避免退化成空种子。
func stableUserTurnAnchor(content gjson.Result) string {
	turnHashes := reusableTurnContentHashes(content)
	if len(turnHashes) == 0 {
		return ""
	}
	start := len(turnHashes) - contentSeedReusableTurnHashes
	if start < 0 {
		start = 0
	}
	var b strings.Builder
	for _, h := range turnHashes[start:] {
		b.WriteString(h)
		b.WriteString(",")
	}
	return b.String()
}

// reusableTurnContentHashes 返回本轮请求中属于“可跨轮复用”的用户输入哈希：
// 字符串正文与数组里 input_text/text 项，按出现顺序。助手输出与工具结果不参与。
func reusableTurnContentHashes(content gjson.Result) []string {
	if !content.Exists() || content.Type == gjson.Null {
		return nil
	}
	hashes := make([]string, 0, 4)
	appendText := func(text string) {
		if text != "" {
			hashes = append(hashes, fmt.Sprintf("%016x", xxhash.Sum64String(text)))
		}
	}
	if content.Type == gjson.String || !content.IsArray() {
		appendText(content.String())
		return hashes
	}
	content.ForEach(func(_, item gjson.Result) bool {
		switch {
		case item.Type == gjson.String:
			appendText(item.String())
		case isReusableUserTextItem(item):
			appendText(item.Get("text").String())
		}
		return true
	})
	return hashes
}

// isReusableUserTextItem 判断一个内容项是不是用户输入文本。
// 助手输出（output_text）与图片等其它项不构成用户锚点，否则「只有助手输出」
// 的请求也会拿到一个锚点，退化成伪会话身份。
func isReusableUserTextItem(item gjson.Result) bool {
	text := item.Get("text")
	if text.Type != gjson.String || text.String() == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.Get("type").String())) {
	case "", "input_text", "text":
		return true
	default:
		return false
	}
}

// splitResponsesContentSeedItems 把 Responses 的 input 切成三段，让种子不随轮次漂移：
//
//	leading  固定前导 system/developer 前缀（并含没有消息角色的顶层 input_text）
//	history  历史轮次（除最后一轮 user 之外的全部消息）
//	turns    最后连续的一轮或多轮 user 消息
//
// 历史里的 developer/user 只作为“用户锚点”输入（走最近复用周期），不参与前缀槽位，
// 因此 DSH 每轮追加一条 developer 运行时上下文时，前缀身份保持不变。
func splitResponsesContentSeedItems(items []gjson.Result) (leading, history, turns []gjson.Result) {
	leading = make([]gjson.Result, 0, 2)
	history = make([]gjson.Result, 0, len(items))
	turns = make([]gjson.Result, 0, 2)

	seenNonPrefix := false
	lastUser := -1
	for i, item := range items {
		role := item.Get("role").String()
		if role == "user" {
			lastUser = i
		}
	}
	// 最后一段连续 user（工具结果之后的一轮可能有多条 user 消息）属于本轮。
	lastRunStart := lastUser
	for lastRunStart > 0 && items[lastRunStart-1].Get("role").String() == "user" {
		lastRunStart--
	}

	for i, item := range items {
		role := item.Get("role").String()
		if !seenNonPrefix {
			switch role {
			case "":
				leading = append(leading, item)
				continue
			case "system", "developer":
				leading = append(leading, item)
				continue
			}
			seenNonPrefix = true
		}
		if lastUser >= 0 && i >= lastRunStart {
			turns = append(turns, item)
			continue
		}
		history = append(history, item)
	}
	return leading, history, turns
}

// captureResponsesSeedAnchor 从一条 Responses input 项里取用户锚点：
// 带 role=user 的消息取其 content；没有消息角色的顶层 input_text 直接取 text
// （官方 Responses 风格）。后者曾经被漏掉，导致「只有 input_text 的请求」退化成
// 只按模型区分，独立提问撞进同一个会话身份。
func captureResponsesSeedAnchor(b *strings.Builder, captured *bool, item gjson.Result) {
	switch role := strings.TrimSpace(item.Get("role").String()); role {
	case "user":
		captureResponsesUserAnchor(b, captured, item.Get("content"))
	case "":
		if strings.EqualFold(strings.TrimSpace(item.Get("type").String()), "input_text") {
			captureResponsesUserAnchor(b, captured, item.Get("text"))
		}
	}
}

// captureResponsesUserAnchor 记录首个用户输入锚点（跨轮稳定的最近复用周期哈希）。
func captureResponsesUserAnchor(b *strings.Builder, captured *bool, content gjson.Result) {
	if *captured {
		return
	}
	*captured = true
	if anchor := stableUserTurnAnchor(content); anchor != "" {
		_, _ = b.WriteString("|first_user=")
		_, _ = b.WriteString(anchor)
	}
}

// deriveOpenAIAnchoredContentSessionSeed returns the legacy content-derived
// seed only when it contains a meaningful user/input anchor. This preserves
// the existing session derivation while preventing model-only requests from
// becoming a tenant-wide cache routing identity.
func deriveOpenAIAnchoredContentSessionSeed(body []byte) string {
	if !hasOpenAIContentSessionUserAnchor(body) {
		return ""
	}
	return deriveOpenAIContentSessionSeed(body)
}

func hasOpenAIContentSessionUserAnchor(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	if messages := gjson.GetBytes(body, "messages"); messages.Exists() && messages.IsArray() {
		anchored := false
		messages.ForEach(func(_, message gjson.Result) bool {
			if strings.TrimSpace(message.Get("role").String()) != "user" {
				return true
			}
			anchored = hasMeaningfulOpenAIContent(message.Get("content"))
			return false
		})
		return anchored
	}

	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	if input.Type == gjson.String {
		return strings.TrimSpace(input.String()) != ""
	}
	if !input.IsArray() {
		return false
	}

	anchored := false
	input.ForEach(func(_, item gjson.Result) bool {
		if strings.TrimSpace(item.Get("role").String()) == "user" {
			anchored = hasMeaningfulOpenAIContent(item.Get("content"))
			return false
		}
		if strings.TrimSpace(item.Get("type").String()) == "input_text" {
			anchored = strings.TrimSpace(item.Get("text").String()) != ""
			return false
		}
		return true
	})
	return anchored
}

func hasMeaningfulOpenAIContent(content gjson.Result) bool {
	if !content.Exists() || content.Type == gjson.Null {
		return false
	}
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String()) != ""
	}
	if !content.IsArray() {
		normalized, ok := normalizeNonEmptyCompatSeedJSON(content)
		return ok && strings.TrimSpace(normalized) != ""
	}

	meaningful := false
	content.ForEach(func(_, item gjson.Result) bool {
		if item.Type == gjson.String {
			meaningful = strings.TrimSpace(item.String()) != ""
		} else if text := item.Get("text"); text.Exists() {
			meaningful = strings.TrimSpace(text.String()) != ""
		} else {
			_, meaningful = normalizeNonEmptyCompatSeedJSON(item)
		}
		return !meaningful
	})
	return meaningful
}

// deriveOpenAIStablePrefixSessionSeed builds a seed from the reusable prefix
// of an OpenAI-format request. User and assistant content are deliberately
// excluded so independent prompts with the same system/tool prefix can share
// an upstream prompt-cache routing identity.
//
// An empty result means the request has no meaningful stable prefix. Callers
// must then use a narrower fallback instead of grouping all requests by tenant
// and model alone.
func deriveOpenAIStablePrefixSessionSeed(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var b strings.Builder
	hasStablePrefix := false
	appendJSON := func(label string, value gjson.Result) {
		normalized, ok := normalizeNonEmptyCompatSeedJSON(value)
		if !ok {
			return
		}
		_, _ = b.WriteString("|")
		_, _ = b.WriteString(label)
		_, _ = b.WriteString("=")
		_, _ = b.WriteString(normalized)
		hasStablePrefix = true
	}

	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && tools.IsArray() {
		appendJSON("tools", tools)
	}
	if funcs := gjson.GetBytes(body, "functions"); funcs.Exists() && funcs.IsArray() {
		appendJSON("functions", funcs)
	}
	if instructions := gjson.GetBytes(body, "instructions"); strings.TrimSpace(instructions.String()) != "" {
		appendJSON("instructions", instructions)
	}

	appendSystemMessages := func(items gjson.Result) {
		items.ForEach(func(_, item gjson.Result) bool {
			role := strings.TrimSpace(item.Get("role").String())
			switch role {
			case "system", "developer":
				appendJSON(role, item.Get("content"))
			}
			return true
		})
	}

	if messages := gjson.GetBytes(body, "messages"); messages.Exists() && messages.IsArray() {
		appendSystemMessages(messages)
	} else if input := gjson.GetBytes(body, "input"); input.Exists() && input.IsArray() {
		appendSystemMessages(input)
	}

	if !hasStablePrefix {
		return ""
	}
	return contentStablePrefixSessionSeedPrefix + b.String()
}

func normalizeNonEmptyCompatSeedJSON(value gjson.Result) (string, bool) {
	if !value.Exists() || value.Type == gjson.Null {
		return "", false
	}
	normalized := normalizeCompatSeedJSON(json.RawMessage(value.Raw))
	switch normalized {
	case "", `""`, "[]", "{}", "null":
		return "", false
	default:
		return normalized, true
	}
}
