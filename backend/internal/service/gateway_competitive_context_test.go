package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompetitiveUpstreamContextPreservesCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx := WithCompetitiveUpstreamCancellation(parent)

	streamCtx, releaseStream := detachStreamUpstreamContext(ctx, true)
	defer releaseStream()
	unaryCtx, releaseUnary := detachUpstreamContext(ctx)
	defer releaseUnary()
	cancel()

	require.ErrorIs(t, streamCtx.Err(), context.Canceled)
	require.ErrorIs(t, unaryCtx.Err(), context.Canceled)
}

func TestNormalStreamingUpstreamContextRemainsDetached(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	streamCtx, release := detachStreamUpstreamContext(parent, true)
	defer release()
	cancel()

	require.NoError(t, streamCtx.Err())
}
