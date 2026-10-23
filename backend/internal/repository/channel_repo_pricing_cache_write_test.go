//go:build unit

package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestChannelCacheWrite1hPricingPersistence(t *testing.T) {
	for _, accountStats := range []bool{false, true} {
		name := "channel"
		if accountStats {
			name = "account stats"
		}
		for _, price := range []*float64{nil, cacheWritePricePointer(0), cacheWritePricePointer(0.000006)} {
			t.Run(name+"/"+cacheWritePriceName(price), func(t *testing.T) {
				repo, mock := newChannelModelPricingTimePricingRepo(t)
				ctx := context.Background()
				pricing := &service.ChannelModelPricing{ChannelID: 7, Platform: "anthropic", Models: []string{"custom-model"}, CacheWrite1hPrice: price,
					Intervals: []service.PricingInterval{{MinTokens: 100, CacheWrite1hPrice: price}}}
				table := "channel_model_pricing"
				intervalTable := "channel_pricing_intervals"
				args := []driver.Value{int64(7), "anthropic", []byte(`["custom-model"]`), service.BillingModeToken, nil, nil, nil, price, nil}
				intervalArgs := []driver.Value{int64(11), int64(100), nil, "", nil, nil, nil, price, nil}
				if accountStats {
					table = "channel_account_stats_model_pricing"
					intervalTable = "channel_account_stats_pricing_intervals"
					args = append(args, "{}", nil, nil)
				} else {
					args = append(args, nil, nil, "{}", nil, nil, nil, nil)
					intervalArgs = append(intervalArgs, nil, nil, nil, nil)
				}
				intervalArgs = append(intervalArgs, nil, int64(0))
				if accountStats {
					mock.ExpectBegin()
				}
				mock.ExpectQuery("INSERT INTO " + table + " .*cache_write_1h_price").WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), time.Time{}, time.Time{}))
				mock.ExpectQuery("(?s)INSERT INTO " + intervalTable + ".*cache_write_1h_price").WithArgs(intervalArgs...).
					WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(12), time.Time{}, time.Time{}))
				if accountStats {
					tx, err := repo.db.BeginTx(ctx, nil)
					require.NoError(t, err)
					require.NoError(t, createAccountStatsModelPricingTx(ctx, tx, 7, pricing))
					mock.ExpectCommit()
					require.NoError(t, tx.Commit())
				} else {
					require.NoError(t, repo.CreateModelPricing(ctx, pricing))
					mock.ExpectExec(`(?s)UPDATE channel_model_pricing.*cache_write_1h_price = \$6.*WHERE id = \$16`).
						WithArgs([]byte(`["custom-model"]`), service.BillingModeToken, nil, nil, nil, price, nil, nil, nil, "{}", nil, nil, nil, nil, "anthropic", int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
					require.NoError(t, repo.UpdateModelPricing(ctx, pricing))
				}
				columns := channelModelPricingTimePricingColumns
				values := []driver.Value{int64(11), int64(7), "anthropic", `["custom-model"]`, service.BillingModeToken, nil, nil, nil, price, nil}
				if accountStats {
					columns = []string{"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_read_price", "reasoning_effort_multipliers", "image_output_price", "per_request_price", "created_at", "updated_at"}
					values = append(values, "{}", nil, nil, time.Time{}, time.Time{})
				} else {
					values = append(values, nil, nil, "{}", nil, nil, nil, nil, time.Time{}, time.Time{})
				}
				mock.ExpectQuery("(?s)SELECT .*cache_write_1h_price.*FROM " + table).WithArgs(pq.Array([]int64{7})).WillReturnRows(sqlmock.NewRows(columns).AddRow(values...))
				intervalColumns := []string{"id", "pricing_id", "min_tokens", "max_tokens", "tier_label", "input_price", "output_price", "cache_write_price", "cache_write_1h_price", "cache_read_price"}
				intervalValues := []driver.Value{int64(12), int64(11), int64(100), nil, "", nil, nil, nil, price, nil}
				if !accountStats {
					intervalColumns = append(intervalColumns, "input_multiplier", "output_multiplier", "cache_write_multiplier", "cache_read_multiplier")
					intervalValues = append(intervalValues, nil, nil, nil, nil)
				}
				intervalColumns = append(intervalColumns, "per_request_price", "sort_order", "created_at", "updated_at")
				intervalValues = append(intervalValues, nil, int64(0), time.Time{}, time.Time{})
				mock.ExpectQuery("(?s)SELECT .*cache_write_1h_price.*FROM " + intervalTable).WithArgs(pq.Array([]int64{11})).WillReturnRows(sqlmock.NewRows(intervalColumns).AddRow(intervalValues...))
				var loaded map[int64][]service.ChannelModelPricing
				var err error
				if accountStats {
					loaded, err = repo.batchLoadAccountStatsModelPricing(ctx, []int64{7})
				} else {
					loaded, err = repo.batchLoadModelPricing(ctx, []int64{7})
				}
				require.NoError(t, err)
				require.Len(t, loaded[7], 1)
				require.Equal(t, price, loaded[7][0].CacheWrite1hPrice)
				require.Len(t, loaded[7][0].Intervals, 1)
				require.Equal(t, price, loaded[7][0].Intervals[0].CacheWrite1hPrice)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func cacheWritePricePointer(value float64) *float64 { return &value }
func cacheWritePriceName(value *float64) string {
	if value == nil {
		return "null"
	}
	if *value == 0 {
		return "zero"
	}
	return "explicit"
}
