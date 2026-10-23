package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// A cross-protocol rejection is decided before any upstream call, so it must be
// classified as a local policy rejection. If it is not, session affinity breaks
// and the scheduler migrates the session to another upstream pool, which loses
// the provider-side prompt cache (cache is scoped per upstream credential).
func TestIsLocalPolicyRejectionCoversCrossProtocolDisable(t *testing.T) {
	require.True(t, IsLocalPolicyRejection(CrossProtocolConversionError{}))
	require.True(t, IsLocalPolicyRejection(fmt.Errorf("wrapped: %w", CrossProtocolConversionError{})))
}

func TestIsLocalPolicyRejectionCoversPolicyBlocks(t *testing.T) {
	require.True(t, IsLocalPolicyRejection(&BetaBlockedError{Message: "beta policy"}))
	require.True(t, IsLocalPolicyRejection(&OpenAIFastBlockedError{Message: "fast policy"}))
	require.True(t, IsLocalPolicyRejection(fmt.Errorf("wrapped: %w", &BetaBlockedError{Message: "x"})))
	require.True(t, IsLocalPolicyRejection(fmt.Errorf("wrapped: %w", &OpenAIFastBlockedError{Message: "x"})))
}

// Genuine upstream failures must NOT be classified as local policy rejections,
// otherwise real account problems would stop demoting bad accounts.
func TestIsLocalPolicyRejectionLeavesUpstreamFailuresAlone(t *testing.T) {
	require.False(t, IsLocalPolicyRejection(nil))
	require.False(t, IsLocalPolicyRejection(errors.New("boom")))
	require.False(t, IsLocalPolicyRejection(context.Canceled))
	require.False(t, IsLocalPolicyRejection(&UpstreamFailoverError{StatusCode: 529}))
	require.False(t, IsLocalPolicyRejection(fmt.Errorf("wrapped: %w", &UpstreamFailoverError{StatusCode: 500})))
}

func TestInboundProtocolFamily(t *testing.T) {
	require.Equal(t, ProtocolFamilyAnthropic, InboundProtocolFamily("/v1/messages"))
	require.Equal(t, ProtocolFamilyAnthropic, InboundProtocolFamily("messages"))
	require.Equal(t, ProtocolFamilyOpenAI, InboundProtocolFamily("/v1/responses"))
	require.Equal(t, ProtocolFamilyOpenAI, InboundProtocolFamily("/v1/chat/completions"))
	require.Equal(t, ProtocolFamilyOpenAI, InboundProtocolFamily("chat_completions"))
	// Unknown endpoints must never cause filtering.
	require.Equal(t, ProtocolFamilyUnknown, InboundProtocolFamily(""))
	require.Equal(t, ProtocolFamilyUnknown, InboundProtocolFamily("/v1/embeddings"))
}

func TestPlatformProtocolFamily(t *testing.T) {
	require.Equal(t, ProtocolFamilyAnthropic, PlatformProtocolFamily(PlatformAnthropic))
	require.Equal(t, ProtocolFamilyOpenAI, PlatformProtocolFamily(PlatformOpenAI))
	require.Equal(t, ProtocolFamilyOpenAI, PlatformProtocolFamily(PlatformCN))
	// Platforms whose upstream format is not certain stay Unknown so routing
	// keeps its current behaviour instead of guessing.
	require.Equal(t, ProtocolFamilyUnknown, PlatformProtocolFamily(PlatformGemini))
	require.Equal(t, ProtocolFamilyUnknown, PlatformProtocolFamily(PlatformAntigravity))
	require.Equal(t, ProtocolFamilyUnknown, PlatformProtocolFamily(""))
	require.Equal(t, ProtocolFamilyUnknown, PlatformProtocolFamily(PlatformComposite))
}

func TestGroupCanServeInboundFamily(t *testing.T) {
	// same family always serves
	require.True(t, GroupCanServeInboundFamily(ProtocolFamilyAnthropic, PlatformAnthropic, false))
	require.True(t, GroupCanServeInboundFamily(ProtocolFamilyOpenAI, PlatformOpenAI, false))

	// cross family needs the switch on
	require.False(t, GroupCanServeInboundFamily(ProtocolFamilyAnthropic, PlatformOpenAI, false),
		"Messages inbound on an OpenAI-family leaf with conversion off is guaranteed to 400")
	require.True(t, GroupCanServeInboundFamily(ProtocolFamilyAnthropic, PlatformOpenAI, true))

	// unknown on either side must not filter
	require.True(t, GroupCanServeInboundFamily(ProtocolFamilyUnknown, PlatformOpenAI, false))
	require.True(t, GroupCanServeInboundFamily(ProtocolFamilyAnthropic, PlatformGemini, false))
}

// Regression for the reported production case: a /responses request routed to an
// anthropic-family leaf whose cross-protocol switch was off. It produced a 400
// and the session then drifted onto another pool, losing the prompt cache.
func TestGroupCanServeInboundFamilyProductionCase(t *testing.T) {
	require.False(t, GroupCanServeInboundFamily(
		InboundProtocolFamily("/responses"), PlatformAnthropic, false))
	// Once the operator enables the switch the leaf is usable again.
	require.True(t, GroupCanServeInboundFamily(
		InboundProtocolFamily("/responses"), PlatformAnthropic, true))
	// A Messages client against an anthropic leaf is never gated.
	require.True(t, GroupCanServeInboundFamily(
		InboundProtocolFamily("/v1/messages"), PlatformAnthropic, false))
}
