package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockOpenAIFastOnCreate(t *testing.T) {
	disabled := false
	enabled := true

	require.True(t, blockOpenAIFastOnCreate(nil))
	require.False(t, blockOpenAIFastOnCreate(&disabled))
	require.True(t, blockOpenAIFastOnCreate(&enabled))
}
