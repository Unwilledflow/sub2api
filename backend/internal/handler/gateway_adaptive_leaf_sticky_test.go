package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Promote moves the remembered leaf to the front and preserves the rest of the
// planned order, so stickiness changes priority without changing eligibility.
func TestPromoteAdaptiveLeaf(t *testing.T) {
	ids := []int64{10, 20, 30, 40}

	require.Equal(t, []int64{30, 10, 20, 40}, promoteAdaptiveLeaf(ids, 30),
		"remembered leaf moves to the front, others keep relative order")

	require.Equal(t, ids, promoteAdaptiveLeaf(ids, 10),
		"already first is a no-op")

	// A leaf the planner excluded must never be re-introduced: it may have gone
	// unavailable, over budget, or outside the user's allowlist.
	require.Equal(t, ids, promoteAdaptiveLeaf(ids, 999),
		"absent leaf must not be added")

	require.Equal(t, []int64(nil), promoteAdaptiveLeaf(nil, 5))
	require.Equal(t, []int64{}, promoteAdaptiveLeaf([]int64{}, 5))
}

// The promoted slice must not alias the caller's backing array, or a later
// append could corrupt the planner's frozen order.
func TestPromoteAdaptiveLeafDoesNotMutateInput(t *testing.T) {
	ids := []int64{10, 20, 30}
	out := promoteAdaptiveLeaf(ids, 30)
	require.Equal(t, []int64{10, 20, 30}, ids, "input order unchanged")
	require.Equal(t, []int64{30, 10, 20}, out)

	out[0] = 999
	require.Equal(t, []int64{10, 20, 30}, ids, "output must not share storage")
}

// Ordering contract the prompt cache depends on: a session whose remembered leaf
// is still eligible keeps it first, so the upstream cache survives an unrelated
// health-score wobble elsewhere in the pool. This is the ccm/kiro case - the
// planner re-ranked a different pool ahead, and without stickiness the session
// would move there and re-read the whole context.
func TestPromoteAdaptiveLeafKeepsSessionOnItsPool(t *testing.T) {
	planned := []int64{30, 20, 10}
	sticky := promoteAdaptiveLeaf(planned, 20)
	require.Equal(t, int64(20), sticky[0],
		"session must stay on its previous pool when that pool is still eligible")
	require.Equal(t, []int64{20, 30, 10}, sticky)
}
