package apicompat

import "strings"

// thinkingTagPairs 列出部分 Chat Completions 上游把推理过程序列化进可见
// content 时使用的包裹标签。桥接层在输出方向剥离它们，客户端不会看到裸的
// 思维链文本。"<thinking>" 必须排在 "<think>" 之前：二者同一起始位置时
// 优先匹配更长的标签。
var thinkingTagPairs = []struct{ open, close string }{
	{open: "<thinking>", close: "</thinking>"},
	{open: "<think>", close: "</think>"},
}

// ThinkingStripper 是流安全的思维链剥离状态机：从 content 流中移除
// "<thinking>…</thinking>" / "<think>…</think>" 块，标签跨 chunk 拆分时
// 也能正确识别；块外的正文原样透传，chunk 末尾的半截标签先缓冲，等下一个
// chunk 消歧后再决定保留还是丢弃。支持标签嵌套（栈式配对）。
type ThinkingStripper struct {
	stack []string // 由内到外的待闭合标签
	hold  string
}

// Process 喂入一个 chunk，返回可见（非思维链）文本。
func (s *ThinkingStripper) Process(chunk string) string {
	data := s.hold + chunk
	s.hold = ""
	var out strings.Builder
	for {
		if len(s.stack) > 0 {
			closeTag := s.stack[len(s.stack)-1]
			ci := strings.Index(data, closeTag)
			oi := -1
			openLen := 0
			oclose := ""
			for _, p := range thinkingTagPairs {
				if i := strings.Index(data, p.open); i >= 0 && (oi < 0 || i < oi) {
					oi, openLen, oclose = i, len(p.open), p.close
				}
			}
			if ci >= 0 && (oi < 0 || ci <= oi) {
				data = data[ci+len(closeTag):]
				s.stack = s.stack[:len(s.stack)-1]
				continue
			}
			if oi >= 0 {
				data = data[oi+openLen:]
				s.stack = append(s.stack, oclose)
				continue
			}
			hold := longestTagPrefixSuffix(data, closeTag, true)
			for _, p := range thinkingTagPairs {
				if t := longestTagPrefixSuffix(data, p.open, false); len(t) > len(hold) {
					hold = t
				}
			}
			s.hold = hold
			return out.String()
		}
		idx := -1
		openLen := 0
		closeTag := ""
		for _, p := range thinkingTagPairs {
			if i := strings.Index(data, p.open); i >= 0 && (idx < 0 || i < idx) {
				idx, openLen, closeTag = i, len(p.open), p.close
			}
		}
		if idx < 0 {
			hold := ""
			for _, p := range thinkingTagPairs {
				if t := longestTagPrefixSuffix(data, p.open, false); len(t) > len(hold) {
					hold = t
				}
			}
			out.WriteString(data[:len(data)-len(hold)])
			s.hold = hold
			return out.String()
		}
		out.WriteString(data[:idx])
		data = data[idx+openLen:]
		s.stack = append(s.stack, closeTag)
	}
}

// Flush 返回流结束时仍被缓冲的正文。若流在思维链块内部结束，未闭合的块
// 连同缓冲一起丢弃（视为推理内容，不下发给客户端）。
func (s *ThinkingStripper) Flush() string {
	if len(s.stack) > 0 {
		s.stack = nil
		s.hold = ""
		return ""
	}
	hold := s.hold
	s.hold = ""
	return hold
}

// StripThinkingBlocks 从非流式字符串中移除思维链块（含嵌套），并裁掉末尾
// 未闭合的块。
func StripThinkingBlocks(s string) string {
	var st ThinkingStripper
	return st.Process(s) + st.Flush()
}

// longestTagPrefixSuffix 返回 tag 的前缀（whole 为 true 时含完整 tag，否则
// 仅真前缀）中同时是 data 后缀的最长部分，用于把跨 chunk 拆分的半截标签
// 留在缓冲区而不是提前下发。
func longestTagPrefixSuffix(data, tag string, whole bool) string {
	max := len(tag)
	if !whole {
		max--
	}
	if max > len(data) {
		max = len(data)
	}
	for l := max; l > 0; l-- {
		if strings.HasSuffix(data, tag[:l]) {
			return tag[:l]
		}
	}
	return ""
}
