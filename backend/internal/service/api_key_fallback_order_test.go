package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeAPIKeyFallbackGroupIDsPreservesPriorityAndRemovesPrimary(t *testing.T) {
	primaryGroupID := int64(17)

	got := normalizeAPIKeyFallbackGroupIDs([]int64{41, 17, 0, 41, -1, 72}, &primaryGroupID)

	require.Equal(t, []int64{41, 72}, got)
}
