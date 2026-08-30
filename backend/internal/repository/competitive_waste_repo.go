package repository

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const createCompetitiveWasteLogSQL = `
	INSERT INTO competitive_waste_logs (
		logical_request_id,
		upstream_request_id,
		user_id,
		api_key_id,
		group_id,
		winner_account_id,
		account_id,
		attempt_no,
		model,
		upstream_model,
		input_tokens,
		output_tokens,
		cache_creation_tokens,
		cache_read_tokens,
		cache_creation_5m_tokens,
		cache_creation_1h_tokens,
		image_output_tokens,
		usage_reported,
		competitive_waste_cost,
		reason,
		duration_ms,
		created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		$12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
	)
	ON CONFLICT (logical_request_id, api_key_id, attempt_no) DO UPDATE SET
		upstream_request_id = COALESCE(NULLIF(EXCLUDED.upstream_request_id, ''), competitive_waste_logs.upstream_request_id),
		input_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.input_tokens ELSE competitive_waste_logs.input_tokens END,
		output_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.output_tokens ELSE competitive_waste_logs.output_tokens END,
		cache_creation_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.cache_creation_tokens ELSE competitive_waste_logs.cache_creation_tokens END,
		cache_read_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.cache_read_tokens ELSE competitive_waste_logs.cache_read_tokens END,
		cache_creation_5m_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.cache_creation_5m_tokens ELSE competitive_waste_logs.cache_creation_5m_tokens END,
		cache_creation_1h_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.cache_creation_1h_tokens ELSE competitive_waste_logs.cache_creation_1h_tokens END,
		image_output_tokens = CASE WHEN EXCLUDED.usage_reported THEN EXCLUDED.image_output_tokens ELSE competitive_waste_logs.image_output_tokens END,
		usage_reported = competitive_waste_logs.usage_reported OR EXCLUDED.usage_reported,
		competitive_waste_cost = COALESCE(EXCLUDED.competitive_waste_cost, competitive_waste_logs.competitive_waste_cost),
		reason = EXCLUDED.reason,
		duration_ms = GREATEST(competitive_waste_logs.duration_ms, EXCLUDED.duration_ms)
`

func (r *usageLogRepository) CreateCompetitiveWasteLogs(ctx context.Context, logs []service.CompetitiveWasteLog) error {
	if r == nil || r.sql == nil || len(logs) == 0 {
		return nil
	}
	for _, log := range logs {
		createdAt := log.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		var groupID any
		if log.GroupID > 0 {
			groupID = log.GroupID
		}
		var upstreamRequestID any
		if value := strings.TrimSpace(log.UpstreamRequestID); value != "" {
			upstreamRequestID = value
		}
		var upstreamModel any
		if value := strings.TrimSpace(log.UpstreamModel); value != "" {
			upstreamModel = value
		}
		if _, err := r.sql.ExecContext(
			ctx,
			createCompetitiveWasteLogSQL,
			log.LogicalRequestID,
			upstreamRequestID,
			log.UserID,
			log.APIKeyID,
			groupID,
			log.WinnerAccountID,
			log.AccountID,
			log.AttemptNo,
			log.Model,
			upstreamModel,
			log.InputTokens,
			log.OutputTokens,
			log.CacheCreationTokens,
			log.CacheReadTokens,
			log.CacheCreation5mTokens,
			log.CacheCreation1hTokens,
			log.ImageOutputTokens,
			log.UsageReported,
			log.CompetitiveWasteCost,
			string(log.Reason),
			log.DurationMs,
			createdAt,
		); err != nil {
			return err
		}
	}
	return nil
}
