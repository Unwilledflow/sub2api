package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageLogUpstreamRequestIDMigration(t *testing.T) {
	content, err := FS.ReadFile("259_add_usage_log_upstream_request_id.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE usage_logs")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS upstream_request_id VARCHAR(128)")
}

func TestUsageLogUpstreamRequestIDIndexMigration(t *testing.T) {
	content, err := FS.ReadFile("260_add_usage_log_upstream_request_id_index_notx.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_request_id")
	require.Contains(t, sql, "ON usage_logs (upstream_request_id)")
	require.Contains(t, sql, "WHERE upstream_request_id IS NOT NULL")
}
