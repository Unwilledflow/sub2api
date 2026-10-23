package claude

import "testing"

func TestIsOpus55(t *testing.T) {
	if !IsOpus55("claude-opus-5-5") {
		t.Fatal("expected claude-opus-5-5 to match")
	}
	if !IsOpus55("anthropic/claude-opus-5-5") {
		t.Fatal("expected provider-prefixed opus 5.5 to match")
	}
	if IsOpus55("claude-opus-5") {
		t.Fatal("claude-opus-5 must not match opus 5.5")
	}
	if IsOpus55("claude-opus-4-5") {
		t.Fatal("claude-opus-4-5 must not match opus 5.5")
	}
}
