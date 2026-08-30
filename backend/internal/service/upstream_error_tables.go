package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// UpstreamErrorClass is Anti-Stall / Adaptive recovery policy for one upstream error.
//
// Sources:
//   - Anthropic: Claude API errors (400 invalid_request, 401 authentication,
//     403 permission, 404 not_found, 413 request_too_large, 429 rate_limit,
//     500 api_error, 529 overloaded_error).
//   - Gemini: Google AI troubleshooting table (INVALID_ARGUMENT, UNAUTHENTICATED,
//     PERMISSION_DENIED, NOT_FOUND, RESOURCE_EXHAUSTED, UNAVAILABLE, INTERNAL,
//     DEADLINE_EXCEEDED, …) plus HTTP 429/5xx paths used by this gateway.
type UpstreamErrorClass string

const (
	// UpstreamClassClientFault: bad request / true client issue — do NOT intercept;
	// returning to client is correct (context length, invalid args, not found).
	UpstreamClassClientFault UpstreamErrorClass = "client_fault"

	// UpstreamClassForceLeaf: account/leaf-scoped capacity or auth — intercept and
	// switch Adaptive leaf immediately (same leaf rarely recovers quickly).
	UpstreamClassForceLeaf UpstreamErrorClass = "force_leaf"

	// UpstreamClassRetryLeaf: transient server-side — intercept; may hold leaf for
	// a few retries before switching.
	UpstreamClassRetryLeaf UpstreamErrorClass = "retry_leaf"

	// UpstreamClassUnknown: not classified; fall back to status heuristics.
	UpstreamClassUnknown UpstreamErrorClass = "unknown"
)

// UpstreamErrorRule is one row in a platform error table.
type UpstreamErrorRule struct {
	// ID stable key for logs / admin display.
	ID string `json:"id"`
	// Platform anthropic | gemini | openai | any
	Platform string `json:"platform"`
	// StatusCodes empty = any HTTP status (match body only).
	StatusCodes []int `json:"status_codes,omitempty"`
	// BodyTypes Anthropic error.type or Gemini error.status (case-insensitive).
	BodyTypes []string `json:"body_types,omitempty"`
	// Keywords optional message substrings (lowercased match).
	Keywords []string `json:"keywords,omitempty"`
	// Class recovery policy.
	Class UpstreamErrorClass `json:"class"`
	// Description human-readable.
	Description string `json:"description"`
}

// AnthropicUpstreamErrorTable is the fine-grained Anthropic / Claude error policy.
// Order matters: first match wins.
var AnthropicUpstreamErrorTable = []UpstreamErrorRule{
	{
		ID:          "anthropic.rate_limit",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusTooManyRequests},
		BodyTypes:   []string{"rate_limit_error"},
		Class:       UpstreamClassForceLeaf,
		Description: "Account/org rate limit (RPM/ITPM/OTPM); switch leaf",
	},
	{
		ID:          "anthropic.overloaded",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{529},
		BodyTypes:   []string{"overloaded_error"},
		Class:       UpstreamClassForceLeaf,
		Description: "Anthropic fleet overloaded (not your quota); switch leaf",
	},
	{
		ID:          "anthropic.auth",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusUnauthorized},
		BodyTypes:   []string{"authentication_error"},
		Class:       UpstreamClassForceLeaf,
		Description: "API key / OAuth credential failure on this leaf",
	},
	{
		ID:          "anthropic.permission",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusForbidden},
		BodyTypes:   []string{"permission_error"},
		Class:       UpstreamClassForceLeaf,
		Description: "Permission denied on this credential/leaf",
	},
	{
		ID:          "anthropic.request_too_large",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusRequestEntityTooLarge},
		BodyTypes:   []string{"request_too_large", "invalid_request_error"},
		Class:       UpstreamClassForceLeaf,
		Description: "Request body too large for this account/path; another leaf may accept",
	},
	{
		ID:          "anthropic.api_error",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
		BodyTypes:   []string{"api_error"},
		Class:       UpstreamClassRetryLeaf,
		Description: "Anthropic internal error; retry same leaf then switch",
	},
	{
		ID:          "anthropic.timeout_5xx",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout},
		Class:       UpstreamClassRetryLeaf,
		Description: "Generic 5xx / timeout from Claude path",
	},
	{
		ID:          "anthropic.not_found",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusNotFound},
		BodyTypes:   []string{"not_found_error"},
		Class:       UpstreamClassClientFault,
		Description: "Resource/model not found — client-facing",
	},
	{
		ID:          "anthropic.invalid_request",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusBadRequest},
		BodyTypes:   []string{"invalid_request_error"},
		// Overloaded sometimes mis-labeled as 400; keywords catch retryable cases.
		Keywords:    []string{"overloaded", "try again", "temporarily"},
		Class:       UpstreamClassRetryLeaf,
		Description: "400 with transient wording → retry; pure invalid_request is client_fault via fallback",
	},
	{
		ID:          "anthropic.invalid_request_strict",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusBadRequest},
		BodyTypes:   []string{"invalid_request_error"},
		Class:       UpstreamClassClientFault,
		Description: "Malformed request / schema error — do not intercept",
	},
	{
		ID:          "anthropic.status_429",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusTooManyRequests},
		Class:       UpstreamClassForceLeaf,
		Description: "HTTP 429 without typed body",
	},
	{
		ID:          "anthropic.status_529",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{529},
		Class:       UpstreamClassForceLeaf,
		Description: "HTTP 529 without typed body",
	},
	{
		ID:          "anthropic.status_401_403",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{http.StatusUnauthorized, http.StatusForbidden},
		Class:       UpstreamClassForceLeaf,
		Description: "Auth/permission status without typed body",
	},
	{
		ID:          "anthropic.status_5xx",
		Platform:    PlatformAnthropic,
		StatusCodes: []int{500, 502, 503, 504},
		Class:       UpstreamClassRetryLeaf,
		Description: "Untyped 5xx",
	},
}

// GeminiUpstreamErrorTable is the fine-grained Gemini / Google AI error policy.
// Matches both HTTP status and google.rpc status strings in JSON body.
var GeminiUpstreamErrorTable = []UpstreamErrorRule{
	{
		ID:          "gemini.resource_exhausted",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusTooManyRequests},
		BodyTypes:   []string{"RESOURCE_EXHAUSTED"},
		Class:       UpstreamClassForceLeaf,
		Description: "Quota / rate limit (RPM/TPM/day); switch leaf",
	},
	{
		ID:          "gemini.resource_exhausted_any_status",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"RESOURCE_EXHAUSTED"},
		Class:       UpstreamClassForceLeaf,
		Description: "RESOURCE_EXHAUSTED on any HTTP code",
	},
	{
		ID:          "gemini.unavailable",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusServiceUnavailable, 529},
		BodyTypes:   []string{"UNAVAILABLE"},
		Class:       UpstreamClassForceLeaf,
		Description: "Service unavailable / capacity; switch leaf",
	},
	{
		ID:          "gemini.unavailable_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"UNAVAILABLE"},
		Class:       UpstreamClassForceLeaf,
		Description: "UNAVAILABLE status string",
	},
	{
		ID:          "gemini.unauthenticated",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusUnauthorized},
		BodyTypes:   []string{"UNAUTHENTICATED"},
		Class:       UpstreamClassForceLeaf,
		Description: "API key / OAuth invalid on this leaf",
	},
	{
		ID:          "gemini.unauthenticated_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"UNAUTHENTICATED"},
		Class:       UpstreamClassForceLeaf,
		Description: "UNAUTHENTICATED status string",
	},
	{
		ID:          "gemini.permission_denied",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusForbidden},
		BodyTypes:   []string{"PERMISSION_DENIED"},
		Class:       UpstreamClassForceLeaf,
		Description: "Permission denied (may be transient on Code Assist OAuth — still switch leaf under Anti-Stall)",
	},
	{
		ID:          "gemini.permission_denied_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"PERMISSION_DENIED"},
		Class:       UpstreamClassForceLeaf,
		Description: "PERMISSION_DENIED status string",
	},
	{
		ID:          "gemini.deadline_exceeded",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusGatewayTimeout, http.StatusRequestTimeout},
		BodyTypes:   []string{"DEADLINE_EXCEEDED"},
		Class:       UpstreamClassRetryLeaf,
		Description: "Upstream deadline exceeded",
	},
	{
		ID:          "gemini.deadline_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"DEADLINE_EXCEEDED"},
		Class:       UpstreamClassRetryLeaf,
		Description: "DEADLINE_EXCEEDED status string",
	},
	{
		ID:          "gemini.internal",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusInternalServerError, http.StatusBadGateway},
		BodyTypes:   []string{"INTERNAL", "UNKNOWN"},
		Class:       UpstreamClassRetryLeaf,
		Description: "Internal / unknown server error",
	},
	{
		ID:          "gemini.internal_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"INTERNAL", "ABORTED", "UNKNOWN"},
		Class:       UpstreamClassRetryLeaf,
		Description: "INTERNAL/ABORTED/UNKNOWN",
	},
	{
		ID:          "gemini.not_found",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusNotFound},
		BodyTypes:   []string{"NOT_FOUND"},
		Class:       UpstreamClassClientFault,
		Description: "Model/resource not found",
	},
	{
		ID:          "gemini.not_found_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"NOT_FOUND"},
		Class:       UpstreamClassClientFault,
		Description: "NOT_FOUND status string",
	},
	{
		ID:          "gemini.invalid_argument",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusBadRequest},
		BodyTypes:   []string{"INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE"},
		Class:       UpstreamClassClientFault,
		Description: "Malformed request / unsupported feature — client-facing",
	},
	{
		ID:          "gemini.invalid_argument_body",
		Platform:    PlatformGemini,
		BodyTypes:   []string{"INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE"},
		Class:       UpstreamClassClientFault,
		Description: "INVALID_ARGUMENT family",
	},
	{
		ID:          "gemini.status_429",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusTooManyRequests},
		Class:       UpstreamClassForceLeaf,
		Description: "HTTP 429 without status string",
	},
	{
		ID:          "gemini.status_529",
		Platform:    PlatformGemini,
		StatusCodes: []int{529},
		Class:       UpstreamClassForceLeaf,
		Description: "HTTP 529 overload",
	},
	{
		ID:          "gemini.status_401_403",
		Platform:    PlatformGemini,
		StatusCodes: []int{http.StatusUnauthorized, http.StatusForbidden},
		Class:       UpstreamClassForceLeaf,
		Description: "Auth/permission HTTP status",
	},
	{
		ID:          "gemini.status_5xx",
		Platform:    PlatformGemini,
		StatusCodes: []int{500, 502, 503, 504},
		Class:       UpstreamClassRetryLeaf,
		Description: "Untyped Gemini 5xx",
	},
}

// ClassifyAnthropicUpstreamError returns policy for Claude/Anthropic upstream errors.
func ClassifyAnthropicUpstreamError(statusCode int, body []byte) UpstreamErrorClass {
	return classifyWithTable(AnthropicUpstreamErrorTable, statusCode, body, extractAnthropicErrorType(body), extractErrorMessageLower(body))
}

// ClassifyGeminiUpstreamError returns policy for Gemini / Google AI upstream errors.
func ClassifyGeminiUpstreamError(statusCode int, body []byte) UpstreamErrorClass {
	return classifyWithTable(GeminiUpstreamErrorTable, statusCode, body, extractGeminiErrorStatus(body), extractErrorMessageLower(body))
}

// ClassifyUpstreamErrorAuto detects body shape (Anthropic vs Gemini vs generic HTTP).
func ClassifyUpstreamErrorAuto(statusCode int, body []byte) UpstreamErrorClass {
	if t := extractAnthropicErrorType(body); t != "" {
		return ClassifyAnthropicUpstreamError(statusCode, body)
	}
	if s := extractGeminiErrorStatus(body); s != "" {
		return ClassifyGeminiUpstreamError(statusCode, body)
	}
	// Generic HTTP heuristics shared across platforms.
	switch statusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusRequestTimeout, http.StatusConflict, http.StatusRequestEntityTooLarge,
		http.StatusTooManyRequests, 529:
		return UpstreamClassForceLeaf
	case 500, 502, 503, 504:
		return UpstreamClassRetryLeaf
	case http.StatusBadRequest, http.StatusNotFound:
		return UpstreamClassClientFault
	default:
		if statusCode >= 500 {
			return UpstreamClassRetryLeaf
		}
		return UpstreamClassUnknown
	}
}

// AntiStallForceLeafFromUpstream is true when policy says force leaf switch.
func AntiStallForceLeafFromUpstream(statusCode int, body []byte) bool {
	return ClassifyUpstreamErrorAuto(statusCode, body) == UpstreamClassForceLeaf
}

// AntiStallShouldInterceptClass is true for force_leaf and retry_leaf (not client_fault).
func AntiStallShouldInterceptClass(class UpstreamErrorClass) bool {
	return class == UpstreamClassForceLeaf || class == UpstreamClassRetryLeaf
}

func classifyWithTable(table []UpstreamErrorRule, statusCode int, body []byte, bodyType, msgLower string) UpstreamErrorClass {
	for _, rule := range table {
		if !statusMatches(rule.StatusCodes, statusCode) {
			continue
		}
		if len(rule.BodyTypes) > 0 {
			if bodyType == "" || !stringInFold(rule.BodyTypes, bodyType) {
				continue
			}
		}
		if len(rule.Keywords) > 0 {
			if msgLower == "" || !keywordsAny(msgLower, rule.Keywords) {
				// Special case: anthropic.invalid_request with keywords is optional
				// soft match — if BodyTypes matched and keywords required but missing,
				// fall through to next rule (strict invalid_request).
				continue
			}
		}
		return rule.Class
	}
	return UpstreamClassUnknown
}

func statusMatches(codes []int, status int) bool {
	if len(codes) == 0 {
		return true
	}
	for _, c := range codes {
		if c == status {
			return true
		}
	}
	return false
}

func stringInFold(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

func keywordsAny(msg string, kws []string) bool {
	for _, kw := range kws {
		if kw != "" && strings.Contains(msg, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// extractAnthropicErrorType reads Claude error JSON: error.type or type under error.
func extractAnthropicErrorType(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	for _, path := range []string{
		"error.type",
		"error.error.type",
		"type",
	} {
		v := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if v != "" && v != "error" {
			return v
		}
	}
	// Nested: {"type":"error","error":{"type":"rate_limit_error",...}}
	if strings.EqualFold(gjson.GetBytes(body, "type").String(), "error") {
		return strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	}
	return ""
}

// extractGeminiErrorStatus reads Google AI style: error.status (RESOURCE_EXHAUSTED, …).
func extractGeminiErrorStatus(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	for _, path := range []string{
		"error.status",
		"status",
		"error.error.status",
	} {
		v := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if v != "" {
			return v
		}
	}
	return ""
}

func extractErrorMessageLower(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	for _, path := range []string{
		"error.message",
		"error.error.message",
		"message",
	} {
		v := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if v != "" {
			return strings.ToLower(v)
		}
	}
	return strings.ToLower(string(body))
}

// ListAnthropicUpstreamErrorTable exposes table for admin/docs (copy).
func ListAnthropicUpstreamErrorTable() []UpstreamErrorRule {
	out := make([]UpstreamErrorRule, len(AnthropicUpstreamErrorTable))
	copy(out, AnthropicUpstreamErrorTable)
	return out
}

// ListGeminiUpstreamErrorTable exposes table for admin/docs (copy).
func ListGeminiUpstreamErrorTable() []UpstreamErrorRule {
	out := make([]UpstreamErrorRule, len(GeminiUpstreamErrorTable))
	copy(out, GeminiUpstreamErrorTable)
	return out
}
