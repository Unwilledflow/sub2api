package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyAnthropicUpstreamError_Table(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   UpstreamErrorClass
	}{
		{
			name:   "rate_limit_error 429",
			status: 429,
			body:   `{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit."}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "overloaded_error 529",
			status: 529,
			body:   `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "authentication_error 401",
			status: 401,
			body:   `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "permission_error 403",
			status: 403,
			body:   `{"type":"error","error":{"type":"permission_error","message":"not allowed"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "api_error 500",
			status: 500,
			body:   `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`,
			want:   UpstreamClassRetryLeaf,
		},
		{
			name:   "not_found 404",
			status: 404,
			body:   `{"type":"error","error":{"type":"not_found_error","message":"Not Found"}}`,
			want:   UpstreamClassClientFault,
		},
		{
			name:   "invalid_request strict 400",
			status: 400,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"messages: text content blocks must contain non-whitespace text"}}`,
			want:   UpstreamClassClientFault,
		},
		{
			name:   "invalid_request transient wording",
			status: 400,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"The service is temporarily overloaded, please try again"}}`,
			want:   UpstreamClassRetryLeaf,
		},
		{
			name:   "bare 429",
			status: 429,
			body:   ``,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "bare 502",
			status: 502,
			body:   ``,
			want:   UpstreamClassRetryLeaf,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyAnthropicUpstreamError(tc.status, []byte(tc.body))
			require.Equal(t, tc.want, got)
		})
	}
}

func TestClassifyGeminiUpstreamError_Table(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   UpstreamErrorClass
	}{
		{
			name:   "RESOURCE_EXHAUSTED 429",
			status: 429,
			body:   `{"error":{"code":429,"message":"You exceeded your current quota","status":"RESOURCE_EXHAUSTED"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "RESOURCE_EXHAUSTED message only",
			status: 429,
			body:   `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "UNAVAILABLE",
			status: 503,
			body:   `{"error":{"code":503,"message":"The service is currently unavailable.","status":"UNAVAILABLE"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "UNAUTHENTICATED",
			status: 401,
			body:   `{"error":{"code":401,"message":"API key not valid","status":"UNAUTHENTICATED"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "PERMISSION_DENIED",
			status: 403,
			body:   `{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`,
			want:   UpstreamClassForceLeaf,
		},
		{
			name:   "DEADLINE_EXCEEDED",
			status: 504,
			body:   `{"error":{"code":504,"message":"Deadline expired","status":"DEADLINE_EXCEEDED"}}`,
			want:   UpstreamClassRetryLeaf,
		},
		{
			name:   "INTERNAL",
			status: 500,
			body:   `{"error":{"code":500,"message":"Internal error","status":"INTERNAL"}}`,
			want:   UpstreamClassRetryLeaf,
		},
		{
			name:   "INVALID_ARGUMENT",
			status: 400,
			body:   `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT"}}`,
			want:   UpstreamClassClientFault,
		},
		{
			name:   "NOT_FOUND",
			status: 404,
			body:   `{"error":{"code":404,"message":"model not found","status":"NOT_FOUND"}}`,
			want:   UpstreamClassClientFault,
		},
		{
			name:   "bare 429",
			status: 429,
			body:   ``,
			want:   UpstreamClassForceLeaf,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyGeminiUpstreamError(tc.status, []byte(tc.body))
			require.Equal(t, tc.want, got)
		})
	}
}

func TestClassifyUpstreamErrorAuto_DetectsShape(t *testing.T) {
	anth := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"rate limit"}}`)
	require.Equal(t, UpstreamClassForceLeaf, ClassifyUpstreamErrorAuto(429, anth))
	require.True(t, AntiStallForceLeafFromUpstream(429, anth))

	gem := []byte(`{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`)
	require.Equal(t, UpstreamClassForceLeaf, ClassifyUpstreamErrorAuto(429, gem))

	require.Equal(t, UpstreamClassClientFault, ClassifyUpstreamErrorAuto(http.StatusBadRequest, nil))
	require.Equal(t, UpstreamClassRetryLeaf, ClassifyUpstreamErrorAuto(502, nil))
}

func TestListErrorTablesNonEmpty(t *testing.T) {
	require.NotEmpty(t, ListAnthropicUpstreamErrorTable())
	require.NotEmpty(t, ListGeminiUpstreamErrorTable())
}
