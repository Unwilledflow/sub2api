package service

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
)

// 协议族边界与跨协议转换开关。
//
// 网关历史上允许「入站协议」与「上游协议」任意交叉。按协议族划分只有两类：
//
//	anthropic 族：POST /v1/messages（Anthropic Messages 线格式）
//	openai 族   ：POST /v1/responses、POST /v1/chat/completions
//
// 族内的 chat <-> responses 互转（Codex OAuth、国产 OpenAI 兼容上游的
// CC→Responses 归一）是上游既有设计，量大且稳定，**不受本开关影响**。
//
// 跨族转换（messages <-> responses / chat）需要整套 Anthropic 事件状态机与
// 回程重建，在 tool_use、thinking 回传、usage 分桶上反复出问题。本开关按分组
// 控制是否允许跨族转换，默认关闭。
//
// 关闭时的行为：不再静默转换，而是返回明确的 400，指出该分组不允许跨协议转换、
// 以及如何开启。这样用户侧看到的是可诊断的错误，而不是「转换漏了参数」这类
// 难以归因的表现。
const (
	// crossProtocolDisabledMessage 是跨协议转换被分组开关关闭时返回给客户端的说明。
	crossProtocolDisabledMessage = "This group does not allow cross-protocol conversion " +
		"(Anthropic Messages <-> OpenAI Responses/Chat Completions). " +
		"Ask the administrator to enable \"cross protocol conversion\" on this group, " +
		"or use a group whose upstream protocol matches your client protocol."

	// crossProtocolDisabledCode 是跨协议转换被关闭时的稳定错误码。
	crossProtocolDisabledCode = "cross_protocol_conversion_disabled"
)

// CrossProtocolConversionAllowed 报告当前请求是否允许跨协议族转换。
//
// 判定顺序：
//  1. 拿不到分组（内部调用、测试、无分组的 key）时放行——此时没有可依据的
//     配置来源，按「保持旧行为」处理，避免误拦。
//  2. 否则读分组级开关。
//
// 注意：本函数只回答「能不能跨族」。族内的 chat<->responses 互转不经过它。
func CrossProtocolConversionAllowed(apiKey *APIKey) bool {
	if apiKey == nil || apiKey.Group == nil {
		return true
	}
	return apiKey.Group.CrossProtocolConversionEnabled
}

// crossProtocolConversionAllowedFromContext 是 CrossProtocolConversionAllowed
// 的 gin 便捷封装，供转发热路径使用。
func crossProtocolConversionAllowedFromContext(c *gin.Context) bool {
	if c == nil {
		return true
	}
	return CrossProtocolConversionAllowed(getAPIKeyFromContext(c))
}

// CrossProtocolConversionError 是跨协议转换被关闭时返回的稳定错误。
type CrossProtocolConversionError struct{}

func (CrossProtocolConversionError) Error() string { return crossProtocolDisabledMessage }

// IsCrossProtocolConversionDisabled 判断错误是否为跨协议转换被分组开关关闭。
//
// Uses errors.As rather than a bare type assertion: several call sites wrap the
// error before it reaches the classification helpers, and a bare assertion would
// silently miss those and treat a local rejection as an upstream failure.
func IsCrossProtocolConversionDisabled(err error) bool {
	var target CrossProtocolConversionError
	return errors.As(err, &target)
}

// IsLocalPolicyRejection reports whether err is a gateway policy rejection that
// was decided before any upstream call happened.
//
// Such an error is not evidence about the selected account: the request never
// reached it. Callers must therefore
//   - keep session affinity (a same-credential retry is still the best cache bet), and
//   - keep it out of account health / scheduler error-rate accounting,
//
// otherwise a purely local rejection silently demotes healthy accounts and lets
// the scheduler migrate the session to another upstream pool, which loses the
// provider-side prompt cache (cache is scoped per upstream credential).
//
// Covered rejections:
//   - BetaBlockedError          Anthropic-side beta policy rule (action=block)
//   - OpenAIFastBlockedError    OpenAI-side fast policy rule (action=block)
//   - CrossProtocolConversionError  group-level cross-protocol switch is off
func IsLocalPolicyRejection(err error) bool {
	if err == nil {
		return false
	}
	var betaErr *BetaBlockedError
	if errors.As(err, &betaErr) {
		return true
	}
	var fastErr *OpenAIFastBlockedError
	if errors.As(err, &fastErr) {
		return true
	}
	return IsCrossProtocolConversionDisabled(err)
}

// ProtocolFamily is one of the two wire-format families the cross-protocol
// switch governs. Callers that route or select upstream accounts can use it to
// avoid picking a pool that provably cannot serve the request.
type ProtocolFamily string

const (
	ProtocolFamilyAnthropic ProtocolFamily = "anthropic"
	ProtocolFamilyOpenAI    ProtocolFamily = "openai"
	// ProtocolFamilyUnknown means "do not make assumptions": callers must treat
	// the request as servable rather than filtering anything out.
	ProtocolFamilyUnknown ProtocolFamily = "unknown"
)

// InboundProtocolFamily maps a client inbound endpoint to its wire-format
// family. The Anthropic family is the Messages format; every other endpoint the
// gateway exposes (Responses, Chat Completions, ...) belongs to the OpenAI
// family. Unrecognised endpoints report Unknown so callers do not filter.
//
// Accepts the endpoint both with and without the version prefix, because
// production error logs record the bare path (e.g. "/responses") while other
// call sites pass the normalised short name ("responses").
func InboundProtocolFamily(endpoint string) ProtocolFamily {
	normalized := strings.ToLower(strings.TrimSpace(endpoint))
	normalized = strings.TrimPrefix(normalized, "/")
	normalized = strings.TrimPrefix(normalized, "v1/")
	switch normalized {
	case "messages":
		return ProtocolFamilyAnthropic
	case "responses", "chat/completions", "chat_completions":
		return ProtocolFamilyOpenAI
	default:
		return ProtocolFamilyUnknown
	}
}

// PlatformProtocolFamily reports the wire format a group's upstream accounts
// speak natively.
//
// Deliberately conservative: platforms whose upstream format is not certain
// (gemini / antigravity / grok / composite / adaptive / empty) report Unknown so
// that routing decisions keep their current behaviour instead of guessing.
func PlatformProtocolFamily(platform string) ProtocolFamily {
	switch strings.TrimSpace(platform) {
	case PlatformAnthropic:
		return ProtocolFamilyAnthropic
	case PlatformOpenAI, PlatformCN,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax,
		PlatformCline, PlatformCommandCode, PlatformVideo, PlatformTypeSafe:
		return ProtocolFamilyOpenAI
	default:
		return ProtocolFamilyUnknown
	}
}

// GroupCanServeInboundFamily reports whether a group can serve a request from
// the given inbound family.
//
// A group whose upstream family matches serves natively. A group from the other
// family can only serve it through cross-protocol conversion, so it qualifies
// only when that switch is enabled on the group. Unknown on either side means
// "assume yes" — the caller keeps its existing ordering and lets the forwarding
// layer produce the real error.
func GroupCanServeInboundFamily(inbound ProtocolFamily, groupPlatform string, crossProtocolEnabled bool) bool {
	if inbound == ProtocolFamilyUnknown {
		return true
	}
	upstream := PlatformProtocolFamily(groupPlatform)
	if upstream == ProtocolFamilyUnknown || upstream == inbound {
		return true
	}
	return crossProtocolEnabled
}
