//go:build unit

package service

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const wrappedForbiddenFixture = `{"response":{"created_at":1791078420,"error":{"code":"upstream_error","message":"Upstream access forbidden, please contact administrator"},"id":"resp_fixture","model":"gpt-6-astra","object":"response","status":"failed"},"sequence_number":8,"type":"response.failed"}`

func TestOpenAIWrappedUpstreamErrorKeepsGatewayFailureSemantics(t *testing.T) {
	payload := []byte(wrappedForbiddenFixture)
	message := extractOpenAISSEErrorMessage(payload)
	require.Equal(t, http.StatusBadGateway, openAIStreamFailureStatus(payload, message), "structured upstream_error must not become a request-level 403 based only on message text")
	require.True(t, openAIStreamFailedEventShouldFailover(payload, message))
	require.True(t, openAIStreamErrorEventShouldFailover(payload, message))
}

func TestOpenAIWrappedUpstreamFailureBeforeOutputReturnsRetrySignal(t *testing.T) {
	preamble := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
		"data: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\n"
	recorder := newOpenAIResponseFlushRecorder()
	_, err := runOpenAIResponseFlushTestWithAccount(recorder, io.NopCloser(strings.NewReader(preamble+"event: response.failed\ndata: "+wrappedForbiddenFixture+"\n\n")), config.GatewayConfig{}, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.ShouldRetryNextAccount())
	require.False(t, failover.IsCredentialFailure(), "a relay error must not permanently disable an otherwise valid credential")
	body, _ := recorder.snapshot()
	require.Empty(t, body, "the failed attempt must remain staged so the handler can select the next account")
}

func TestOpenAIWrappedUpstreamFailurePreambleCommitPolicy(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		t.Run(accountType, func(t *testing.T) {
			recorder := newOpenAIResponseFlushRecorder()
			body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\"}}\n\n" +
				"data: " + wrappedForbiddenFixture + "\n\n"
			_, err := runOpenAIResponseFlushTestWithAccount(recorder, io.NopCloser(strings.NewReader(body)), config.GatewayConfig{}, &Account{ID: 1, Platform: PlatformOpenAI, Type: accountType})
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.ShouldRetryNextAccount())
			require.False(t, failover.IsCredentialFailure())
			out, _ := recorder.snapshot()
			if accountType == AccountTypeAPIKey {
				require.Empty(t, out)
			} else {
				require.Contains(t, out, `"type":"response.created"`)
				require.True(t, failover.SafeToFailoverAfterWrite)
			}
			require.NotContains(t, out, "response.failed")
		})
	}
}

func TestOpenAIWrappedUpstreamFailureAfterContentNeverReplays(t *testing.T) {
	for _, content := range []string{
		`{"type":"response.output_text.delta","delta":"already delivered"}`,
		`{"type":"response.function_call_arguments.delta","delta":"{\"path\":"}`,
	} {
		for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
			recorder := newOpenAIResponseFlushRecorder()
			_, err := runOpenAIResponseFlushTestWithAccount(recorder, io.NopCloser(strings.NewReader("data: "+content+"\n\ndata: "+wrappedForbiddenFixture+"\n\n")), config.GatewayConfig{}, &Account{ID: 1, Platform: PlatformOpenAI, Type: accountType})
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			body, _ := recorder.snapshot()
			require.Contains(t, body, content)
			require.Contains(t, body, "response.failed")
		}
	}
}

func TestOpenAIWrappedUpstreamErrorDoesNotOverrideRequestRestrictions(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","status_code":403,"message":"forbidden"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","type":"permission_error","message":"forbidden"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","message":"content_policy: forbidden request"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"upstream_error","message":"context_length_exceeded"}}}`,
	} {
		message := extractOpenAISSEErrorMessage([]byte(payload))
		require.False(t, openAIStreamFailedEventShouldFailover([]byte(payload), message), payload)
		require.False(t, openAIStreamErrorEventShouldFailover([]byte(payload), message), payload)
	}
}
