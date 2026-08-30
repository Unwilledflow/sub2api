package service

import (
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAccountSchedulingBounds(t *testing.T) {
	for _, value := range []int{AccountPriorityMin, 1, AccountPriorityMax} {
		require.NoError(t, ValidateAccountPriority(value))
	}
	for _, value := range []int{AccountPriorityMin - 1, AccountPriorityMax + 1, 1_000_000} {
		err := ValidateAccountPriority(value)
		require.Equal(t, 400, infraerrors.Code(err))
	}

	for _, value := range []int{0, AccountLoadFactorMin, AccountLoadFactorMax} {
		require.NoError(t, ValidateAccountLoadFactor(value, true))
	}
	for _, value := range []int{-1, AccountLoadFactorMax + 1, 1_000_000} {
		err := ValidateAccountLoadFactor(value, true)
		require.Equal(t, 400, infraerrors.Code(err))
	}
	require.Error(t, ValidateAccountLoadFactor(0, false))
}

func TestClampAccountSchedulingValues(t *testing.T) {
	require.Equal(t, AccountPriorityMin, ClampAccountPriority(-1))
	require.Equal(t, 7, ClampAccountPriority(7))
	require.Equal(t, AccountPriorityMax, ClampAccountPriority(7_000))

	require.Zero(t, ClampAccountLoadFactor(-1))
	require.Zero(t, ClampAccountLoadFactor(0))
	require.Equal(t, 125, ClampAccountLoadFactor(125))
	require.Equal(t, AccountLoadFactorMax, ClampAccountLoadFactor(7_000))
}
