package service

import (
	"context"
	"testing"
	"time"
)

func TestWithHTTPUpstreamProfile_DefaultKeepsContext(t *testing.T) {
	ctx := context.Background()
	got := WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileDefault)
	if got != ctx {
		t.Fatal("default profile should not wrap context")
	}
}

func TestWithHTTPUpstreamProfile_OpenAI(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.TODO(), HTTPUpstreamProfileOpenAI)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileOpenAI {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileOpenAI, profile)
	}
}

func TestWithHTTPUpstreamResponseHeaderTimeout(t *testing.T) {
	base := context.Background()
	if got := WithHTTPUpstreamResponseHeaderTimeout(base, 0); got != base {
		t.Fatal("non-positive timeout should not wrap context")
	}

	ctx := WithHTTPUpstreamResponseHeaderTimeout(base, 12*time.Second)
	timeout, ok := HTTPUpstreamResponseHeaderTimeoutFromContext(ctx)
	if !ok {
		t.Fatal("expected response header timeout override")
	}
	if timeout != 12*time.Second {
		t.Fatalf("timeout = %v, want 12s", timeout)
	}
}

func TestWithHTTPUpstreamRedirectsDisabled(t *testing.T) {
	//nolint:staticcheck // Exercises the defensive nil-context fallback.
	ctx := WithHTTPUpstreamRedirectsDisabled(nil)
	if !HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("expected redirects to be disabled")
	}
	if HTTPUpstreamRedirectsDisabled(context.Background()) {
		t.Fatal("redirects should remain enabled by default")
	}
}
