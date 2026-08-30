package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const accountFailureCountPrefix = "acct_fail_count:"
const accountFailureSamplePrefix = "acct_fail_sample:"
const accountFailureMultiplierPrefix = "acct_fail_mult:"

var accountFailureIncrScript = redis.NewScript(`
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

var accountFailureBumpMultiplierScript = redis.NewScript(`
	local key = KEYS[1]
	local debounce_ttl = tonumber(ARGV[1])
	local max_mult = tonumber(ARGV[2])

	local mult = redis.call('INCR', key)
	if mult == 1 then
		redis.call('EXPIRE', key, debounce_ttl)
	end
	if max_mult > 0 and mult > max_mult then
		redis.call('SET', key, max_mult, 'EX', debounce_ttl)
		return max_mult
	end
	return mult
`)

type accountFailureCounterCache struct {
	rdb *redis.Client
}

// NewAccountFailureCounterCache creates the Redis-backed consecutive failure counter.
func NewAccountFailureCounterCache(rdb *redis.Client) service.AccountFailureCounterCache {
	return &accountFailureCounterCache{rdb: rdb}
}

func (c *accountFailureCounterCache) ObserveAccountFailure(ctx context.Context, accountID int64, windowSeconds, sampleIntervalSeconds int) (int64, bool, error) {
	if windowSeconds < 1 {
		windowSeconds = 1
	}
	if sampleIntervalSeconds < 1 {
		sampleIntervalSeconds = 1
	}
	countKey := fmt.Sprintf("%s%d", accountFailureCountPrefix, accountID)
	sampleKey := fmt.Sprintf("%s%d", accountFailureSamplePrefix, accountID)
	encoded, err := accountFailureIncrScript.Run(ctx, c.rdb, []string{countKey, sampleKey}, windowSeconds, sampleIntervalSeconds).Int64()
	if err != nil {
		return 0, false, fmt.Errorf("observe account failure: %w", err)
	}
	return encoded / 2, encoded%2 == 1, nil
}

func (c *accountFailureCounterCache) ResetAccountFailureCount(ctx context.Context, accountID int64) error {
	return c.rdb.Del(ctx, fmt.Sprintf("%s%d", accountFailureCountPrefix, accountID)).Err()
}

func (c *accountFailureCounterCache) ClearAccountFailureState(ctx context.Context, accountID int64) error {
	countKey := fmt.Sprintf("%s%d", accountFailureCountPrefix, accountID)
	sampleKey := fmt.Sprintf("%s%d", accountFailureSamplePrefix, accountID)
	return c.rdb.Del(ctx, countKey, sampleKey).Err()
}

func (c *accountFailureCounterCache) CurrentMultiplier(ctx context.Context, accountID int64) (int, error) {
	key := fmt.Sprintf("%s%d", accountFailureMultiplierPrefix, accountID)
	mult, err := c.rdb.Get(ctx, key).Int()
	if err != nil {
		if err == redis.Nil {
			return 1, nil
		}
		return 1, fmt.Errorf("get account failure multiplier: %w", err)
	}
	if mult < 1 {
		return 1, nil
	}
	return mult, nil
}

func (c *accountFailureCounterCache) BumpMultiplier(ctx context.Context, accountID int64, debounceSeconds, maxMultiplier int) (int, error) {
	if debounceSeconds < 1 {
		debounceSeconds = 1800
	}
	if maxMultiplier < 1 {
		maxMultiplier = 8
	}
	key := fmt.Sprintf("%s%d", accountFailureMultiplierPrefix, accountID)
	mult, err := accountFailureBumpMultiplierScript.Run(ctx, c.rdb, []string{key}, debounceSeconds, maxMultiplier).Int()
	if err != nil {
		return 0, fmt.Errorf("bump account failure multiplier: %w", err)
	}
	return mult, nil
}
