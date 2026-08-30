package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const openAIPool5xxCounterPrefix = "openai_pool_5xx_count:account:"
const openAIPool5xxSamplePrefix = "openai_pool_5xx_sample:account:"

var openAIPool5xxCounterIncrScript = redis.NewScript(`
	local count_key = KEYS[1]
	local sample_key = KEYS[2]
	local count_ttl = tonumber(ARGV[1])
	local sample_ttl = tonumber(ARGV[2])

	local sampled = redis.call('SET', sample_key, '1', 'EX', sample_ttl, 'NX')
	if not sampled then
		local current = tonumber(redis.call('GET', count_key) or '0')
		return current * 2
	end

	local count = redis.call('INCR', count_key)
	if count == 1 then
		redis.call('EXPIRE', count_key, count_ttl)
	end

	return count * 2 + 1
`)

type openAIPool5xxCounterCache struct {
	rdb *redis.Client
}

func NewOpenAIPool5xxCounterCache(rdb *redis.Client) service.OpenAIPool5xxCounterCache {
	return &openAIPool5xxCounterCache{rdb: rdb}
}

func (c *openAIPool5xxCounterCache) ObserveOpenAIPool5xxFailure(ctx context.Context, accountID int64, windowSeconds, sampleIntervalSeconds int) (int64, bool, error) {
	countKey := fmt.Sprintf("%s%d", openAIPool5xxCounterPrefix, accountID)
	sampleKey := fmt.Sprintf("%s%d", openAIPool5xxSamplePrefix, accountID)
	if windowSeconds < 1 {
		windowSeconds = 1
	}
	if sampleIntervalSeconds < 1 {
		sampleIntervalSeconds = 1
	}
	encoded, err := openAIPool5xxCounterIncrScript.Run(ctx, c.rdb, []string{countKey, sampleKey}, windowSeconds, sampleIntervalSeconds).Int64()
	if err != nil {
		return 0, false, fmt.Errorf("observe openai pool 5xx failure: %w", err)
	}
	return encoded / 2, encoded%2 == 1, nil
}

func (c *openAIPool5xxCounterCache) ResetOpenAIPool5xxCount(ctx context.Context, accountID int64) error {
	key := fmt.Sprintf("%s%d", openAIPool5xxCounterPrefix, accountID)
	return c.rdb.Del(ctx, key).Err()
}

func (c *openAIPool5xxCounterCache) ClearOpenAIPool5xxState(ctx context.Context, accountID int64) error {
	countKey := fmt.Sprintf("%s%d", openAIPool5xxCounterPrefix, accountID)
	sampleKey := fmt.Sprintf("%s%d", openAIPool5xxSamplePrefix, accountID)
	return c.rdb.Del(ctx, countKey, sampleKey).Err()
}
