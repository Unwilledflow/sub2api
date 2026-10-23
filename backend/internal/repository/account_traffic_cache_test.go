package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// trafficFixture returns the miniredis clock alongside the cache so tests can
// advance time explicitly; miniredis exposes no readable "now".
func trafficFixture(t *testing.T) (*miniredis.Miniredis, *trafficClock, *accountTrafficCache, service.AccountTrafficPlan) {
	t.Helper()
	server := miniredis.RunT(t)
	start := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	server.SetTime(start)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { client.Close() })
	p := service.DefaultAccountTrafficPolicy()
	p.StrictRPMEnabled = true
	p.RPM = 3
	p.Burst = 3
	clock := &trafficClock{server: server, current: start}
	return server, clock, &accountTrafficCache{rdb: client}, service.AccountTrafficPlan{AccountID: 42, Revision: 1, Signature: "policy-1", HardLimit: 200, Policy: p}
}

// trafficClock advances the fake Redis clock the Lua scripts read via TIME.
type trafficClock struct {
	server  *miniredis.Miniredis
	current time.Time
}

func (c *trafficClock) advance(d time.Duration) {
	c.current = c.current.Add(d)
	c.server.SetTime(c.current)
}

// trafficFloor is the fixed adaptive floor the backend pins on every policy.
const trafficFloor = 50

func TestAccountTrafficAtomicRPMAndBurst(t *testing.T) {
	_, clock, cache, plan := trafficFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		a, err := cache.Acquire(ctx, plan, fmt.Sprint(i))
		require.NoError(t, err)
		require.True(t, a.Allowed)
		require.NoError(t, cache.Finish(ctx, plan, fmt.Sprint(i), 200, 100))
	}
	blocked, err := cache.Acquire(ctx, plan, "fourth")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, time.Minute, blocked.RetryAfter)
	clock.advance(20 * time.Second)
	blocked, err = cache.Acquire(ctx, plan, "still-full")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, 40*time.Second, blocked.RetryAfter)
	clock.advance(41 * time.Second)
	accepted, err := cache.Acquire(ctx, plan, "new-window")
	require.NoError(t, err)
	require.True(t, accepted.Allowed)
	state, err := cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 1, state.RequestsLastMinute)
	require.EqualValues(t, 4, state.Accepted)
	// A burst smaller than RPM stops a simultaneous spike before the minute cap.
	plan.AccountID = 43
	plan.Policy.RPM = 60
	plan.Policy.Burst = 2
	for i := 0; i < 2; i++ {
		a, err := cache.Acquire(ctx, plan, fmt.Sprint(i))
		require.NoError(t, err)
		require.True(t, a.Allowed)
	}
	blocked, err = cache.Acquire(ctx, plan, "burst-full")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, time.Second, blocked.RetryAfter)
}

func TestAccountTrafficConcurrentInstancesCannotExceedCeiling(t *testing.T) {
	server, _, cache, plan := trafficFixture(t)
	plan.Policy.RPM = 1
	plan.Policy.Burst = 1
	otherClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer otherClient.Close()
	other := &accountTrafficCache{rdb: otherClient}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			chosen := cache
			if i%2 == 1 {
				chosen = other
			}
			a, err := chosen.Acquire(context.Background(), plan, fmt.Sprint(i))
			if err != nil {
				failures <- err
			}
			if a.Allowed {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, admitted.Load())
}

func TestAccountTrafficAdaptiveSuggestionAndFastRecovery(t *testing.T) {
	_, clock, cache, plan := trafficFixture(t)
	ctx := context.Background()
	plan.Policy.StrictRPMEnabled = false
	plan.Policy.AdaptiveEnabled = true
	plan.Policy.FailureThreshold = 2
	plan.Policy.AdaptiveMode = "observe"
	for i := 0; i < 2; i++ {
		id := fmt.Sprint(i)
		a, err := cache.Acquire(ctx, plan, id)
		require.NoError(t, err)
		require.True(t, a.Allowed)
		require.NoError(t, cache.Finish(ctx, plan, id, 429, 100))
	}
	state, err := cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 150, state.RecommendedConcurrency, "a 429 backs off by a quarter, not by half")
	require.Equal(t, 200, state.EffectiveConcurrency, "observe mode never lowers the actual cap")
	plan.Revision = 2
	plan.Signature = "automatic"
	plan.Policy.AdaptiveMode = "automatic"
	require.NoError(t, cache.Sync(ctx, plan))
	for i := 0; i < 150; i++ {
		a, err := cache.Acquire(ctx, plan, fmt.Sprint("active-", i))
		require.NoError(t, err)
		require.True(t, a.Allowed)
	}
	blocked, err := cache.Acquire(ctx, plan, "over-limit")
	require.NoError(t, err)
	require.False(t, blocked.Allowed)
	require.Equal(t, 150, blocked.State.EffectiveConcurrency)
	for i := 0; i < 3; i++ {
		require.NoError(t, cache.Finish(ctx, plan, fmt.Sprint("active-", i), 200, 100))
	}
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 150, state.EffectiveConcurrency, "the recovery window has not elapsed yet")
	clock.advance(61 * time.Second)
	require.NoError(t, cache.Finish(ctx, plan, "active-3", 200, 100))
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 175, state.EffectiveConcurrency, "a healthy window closes half the gap to the ceiling")
	require.Equal(t, 200, plan.HardLimit, "the saved administrator cap is not mutated")
	plan.Policy.AdaptiveEnabled = false
	plan.Revision = 3
	plan.Signature = "disabled"
	require.NoError(t, cache.Sync(ctx, plan))
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 200, state.EffectiveConcurrency)
}

// A burst of upstream 5xx used to halve concurrency: 502s from a gateway hop or
// a dead credential removed capacity exactly when the pool needed to fail over,
// and the pool never climbed back. 5xx must be counted, never throttled.
func TestAccountTrafficUpstream5xxDoesNotShrinkConcurrency(t *testing.T) {
	_, clock, cache, plan := trafficFixture(t)
	ctx := context.Background()
	plan.Policy.StrictRPMEnabled = false
	plan.Policy.AdaptiveEnabled = true
	plan.Policy.AdaptiveMode = "automatic"
	plan.Policy.FailureThreshold = 1
	for i := 0; i < 6; i++ {
		id := fmt.Sprint("bad-", i)
		a, err := cache.Acquire(ctx, plan, id)
		require.NoError(t, err)
		require.True(t, a.Allowed)
		require.NoError(t, cache.Finish(ctx, plan, id, 502, 100))
		clock.advance(11 * time.Second)
	}
	state, err := cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 200, state.RecommendedConcurrency, "5xx never lowers the recommendation")
	require.Equal(t, 200, state.EffectiveConcurrency)
	require.EqualValues(t, 6, state.Upstream5xx, "5xx is still counted for the dashboard")

	// And a 502 must not restart the health clock that gates recovery.
	clock.advance(2 * time.Minute)
	for i := 0; i < 3; i++ {
		id := fmt.Sprint("good-", i)
		a, err := cache.Acquire(ctx, plan, id)
		require.NoError(t, err)
		require.True(t, a.Allowed)
		require.NoError(t, cache.Finish(ctx, plan, id, 200, 100))
		clock.advance(11 * time.Second)
	}
	state, err = cache.Snapshot(ctx, plan)
	require.NoError(t, err)
	require.Equal(t, 200, state.EffectiveConcurrency)
}

// A recommendation left below the floor by an older controller must be lifted
// on the next admission instead of slowly climbing back one slot at a time.
func TestAccountTrafficCrushedRecommendationRecoversToFloor(t *testing.T) {
	_, clock, cache, plan := trafficFixture(t)
	ctx := context.Background()
	plan.Policy.StrictRPMEnabled = false
	plan.Policy.AdaptiveEnabled = true
	plan.Policy.AdaptiveMode = "automatic"
	plan.HardLimit = 0
	require.NoError(t, cache.Sync(ctx, plan))
	cache.rdb.HSet(ctx, trafficKeys(plan.AccountID)[0], "recommended", 1)
	clock.advance(2 * time.Minute)
	next, err := cache.Acquire(ctx, plan, "after-crush")
	require.NoError(t, err)
	require.True(t, next.Allowed)
	require.GreaterOrEqual(t, next.State.EffectiveConcurrency, trafficFloor, "the floor keeps a crushed pool usable")
}

func TestAccountTrafficAdaptiveWorksWithoutHardCap(t *testing.T) {
	_, _, cache, plan := trafficFixture(t)
	ctx := context.Background()
	plan.HardLimit = 0
	plan.Policy.StrictRPMEnabled = false
	plan.Policy.AdaptiveEnabled = true
	plan.Policy.AdaptiveMode = "automatic"
	plan.Policy.FailureThreshold = 1
	first, err := cache.Acquire(ctx, plan, "one")
	require.NoError(t, err)
	require.True(t, first.Allowed)
	require.Equal(t, 64, first.State.EffectiveConcurrency)
	require.NoError(t, cache.Finish(ctx, plan, "one", 429, 50))
	second, err := cache.Acquire(ctx, plan, "two")
	require.NoError(t, err)
	require.True(t, second.Allowed)
	require.GreaterOrEqual(t, second.State.EffectiveConcurrency, trafficFloor)
}

func TestAccountTrafficStalePolicyAndOldCompletionCannotUndoChanges(t *testing.T) {
	_, _, cache, old := trafficFixture(t)
	ctx := context.Background()
	old.Policy.StrictRPMEnabled = false
	old.Policy.AdaptiveEnabled = true
	old.Policy.FailureThreshold = 1
	old.Policy.AdaptiveMode = "observe"
	a, err := cache.Acquire(ctx, old, "old-request")
	require.NoError(t, err)
	require.True(t, a.Allowed)
	updated := old
	updated.Revision = 2
	updated.Signature = "turned-off"
	updated.Policy.AdaptiveEnabled = false
	require.NoError(t, cache.Sync(ctx, updated))
	blocked, err := cache.Acquire(ctx, old, "stale")
	require.NoError(t, err)
	require.True(t, blocked.Allowed)
	require.True(t, blocked.SkipObservation, "turning off observation must not block an old session")
	require.NoError(t, cache.Finish(ctx, old, "old-request", 429, 50))
	state, err := cache.Snapshot(ctx, updated)
	require.NoError(t, err)
	require.Equal(t, 200, state.EffectiveConcurrency)
	require.Equal(t, 200, state.RecommendedConcurrency)
	require.Zero(t, state.InFlight)
}
