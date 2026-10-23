package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// WrapUpstreamErrorForClient keeps the upstream HTTP status and message.
// The gateway only wraps them into the inbound error envelope; it does not
// rewrite 503 overloaded into a generic 502 "Upstream request failed".
func WrapUpstreamErrorForClient(statusCode int, body []byte) (status int, errType, errCode, message string) {
	status = statusCode
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	// 上游余额/额度类错误不能把原状态码（尤其 400/402/403）透给客户端：
	// 一是暴露上游计费细节，二是客户端会把「上游欠费」误判成自己的请求错误。
	// 这类错误一律折成 502 + 通用不可用文案，计费与冷却由
	// GuardUpstreamFinancialError 另行处理。
	if IsUpstreamFinancialError(statusCode, body) {
		return http.StatusBadGateway, "upstream_error", "", UpstreamUnavailableMessage
	}

	message = strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(body)))
	if message == "" {
		plain := strings.TrimSpace(string(body))
		if plain != "" && !strings.HasPrefix(plain, "{") && !strings.HasPrefix(plain, "<") {
			message = strings.TrimSpace(sanitizeUpstreamErrorMessage(plain))
		}
	}
	if message == "" {
		if text := http.StatusText(status); text != "" {
			message = text
		} else {
			message = "upstream error"
		}
	}

	errType = strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	if errType == "" {
		errType = strings.TrimSpace(gjson.GetBytes(body, "error.status").String())
	}
	if mappedStatus := openAIDeterministicClientHTTPStatus(message, body); mappedStatus > 0 {
		if status < 400 || status >= 500 {
			status = mappedStatus
		}
		if errType == "" || errType == "api_error" || errType == "upstream_error" {
			errType = openAIDeterministicClientErrorType(status)
		}
	}
	if errType == "" {
		switch status {
		case http.StatusTooManyRequests:
			errType = "rate_limit_error"
		case 529:
			errType = "overloaded_error"
		case http.StatusBadRequest:
			errType = "invalid_request_error"
		case http.StatusNotFound:
			errType = "not_found_error"
		default:
			errType = "api_error"
		}
	}
	errCode = extractUpstreamErrorCode(body)
	return
}
