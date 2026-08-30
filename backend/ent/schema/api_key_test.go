package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyBlockOpenAIFastDefaultsOn(t *testing.T) {
	for _, apiKeyField := range (APIKey{}).Fields() {
		descriptor := apiKeyField.Descriptor()
		if descriptor.Name == "block_openai_fast" {
			require.Equal(t, true, descriptor.Default)
			return
		}
	}

	t.Fatal("block_openai_fast field not found")
}
