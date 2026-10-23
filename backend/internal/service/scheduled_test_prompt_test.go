package service

import "testing"

func TestIsDefaultScheduledTestPrompt(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   bool
	}{
		{name: "empty", prompt: "", want: true},
		{name: "spaces", prompt: "  \n", want: true},
		{name: "backend default", prompt: DefaultScheduledTestPrompt, want: true},
		{name: "261 ui short", prompt: LegacyScheduledTestPrompt, want: true},
		{name: "custom", prompt: "Reply pong", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDefaultScheduledTestPrompt(tc.prompt); got != tc.want {
				t.Fatalf("isDefaultScheduledTestPrompt(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestAssessScheduledTestQualityAcceptsLegacyPrompt(t *testing.T) {
	status, reason := assessScheduledTestQuality("pong", LegacyScheduledTestPrompt)
	if status != "degraded" {
		t.Fatalf("legacy prompt status = %s (%s), want degraded", status, reason)
	}
	status, _ = assessScheduledTestQuality("pong", "Reply pong")
	if status != "unknown" {
		t.Fatalf("custom prompt status = %s, want unknown", status)
	}
}
