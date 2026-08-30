package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestOpenAIPool5xxCounterCacheCoalescesBurstAndResets(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewOpenAIPool5xxCounterCache(client)
	ctx := context.Background()

	type observation struct {
		count   int64
		sampled bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan observation, 100)
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			count, sampled, err := cache.ObserveOpenAIPool5xxFailure(ctx, 625, 60, 10)
			results <- observation{count: count, sampled: sampled, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	sampledCount := 0
	for result := range results {
		require.NoError(t, result.err)
		require.Equal(t, int64(1), result.count)
		if result.sampled {
			sampledCount++
		}
	}
	require.Equal(t, 1, sampledCount)

	server.FastForward(10 * time.Second)
	count, sampled, err := cache.ObserveOpenAIPool5xxFailure(ctx, 625, 60, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	require.True(t, sampled)

	ttl := server.TTL(openAIPool5xxCounterPrefix + "625")
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, time.Minute)

	require.NoError(t, cache.ResetOpenAIPool5xxCount(ctx, 625))
	count, sampled, err = cache.ObserveOpenAIPool5xxFailure(ctx, 625, 60, 10)
	require.NoError(t, err)
	require.Zero(t, count)
	require.False(t, sampled, "reset must retain the sample lock for in-flight responses")

	require.NoError(t, cache.ClearOpenAIPool5xxState(ctx, 625))
	count, sampled, err = cache.ObserveOpenAIPool5xxFailure(ctx, 625, 60, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.True(t, sampled, "recovery must clear both the count and sample keys")
}

func TestOpenAIPool5xxCounterCacheSuccessBreaksFailureStreak(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewOpenAIPool5xxCounterCache(client)
	ctx := context.Background()

	count, sampled, err := cache.ObserveOpenAIPool5xxFailure(ctx, 625, 60, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
	require.True(t, sampled)

	require.NoError(t, cache.ResetOpenAIPool5xxCount(ctx, 625))
	server.FastForward(10 * time.Second)

	count, sampled, err = cache.ObserveOpenAIPool5xxFailure(ctx, 625, 60, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), count, "a failure after a successful response must start a new streak")
	require.True(t, sampled)
}
