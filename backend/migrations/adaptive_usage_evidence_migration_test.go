package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdaptiveUsageEvidenceMigration_EnforcesReservationAttemptUniqueness(t *testing.T) {
	content, err := FS.ReadFile("189_adaptive_usage_log_metadata.sql")
	require.NoError(t, err)
	sqlText := string(content)

	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS adaptive_usage_evidence_keys",
		"PRIMARY KEY (reservation_id, attempt_no)",
		"GROUP BY adaptive_reservation_id, adaptive_attempt_no",
		"INSERT INTO adaptive_usage_evidence_keys",
		"NEW.adaptive_reservation_id, NEW.adaptive_attempt_no, NEW.id, NEW.created_at",
	} {
		require.Contains(t, sqlText, required)
	}
	require.NotContains(t, sqlText, "CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_logs_adaptive_reservation_created")
	require.NotContains(t, sqlText, "CREATE INDEX IF NOT EXISTS idx_usage_logs_adaptive_reservation_attempt")

	// The lookup index is built outside the transaction; the evidence table's
	// primary key above remains the global uniqueness fence.
	content, err = FS.ReadFile("189_adaptive_usage_log_metadata_notx.sql")
	require.NoError(t, err)
	indexSQL := string(content)
	require.Contains(t, indexSQL, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_adaptive_reservation_attempt")
	require.Contains(t, indexSQL, "ON usage_logs (adaptive_reservation_id, adaptive_attempt_no)")
	require.Contains(t, indexSQL, "WHERE adaptive_reservation_id IS NOT NULL")
}

func TestAdaptiveReservationMigration_RequiresPositiveParentGroup(t *testing.T) {
	content, err := FS.ReadFile("188_adaptive_billing_reservations.sql")
	require.NoError(t, err)
	sqlText := string(content)

	require.Contains(t, sqlText, "parent_group_id                 BIGINT NOT NULL")
	require.Contains(t, sqlText, "CHECK ((parent_group_id > 0")
}
