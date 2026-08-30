package repository

import (
	"database/sql"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrepareUsageLogInsertIncludesUpstreamResponseAudit(t *testing.T) {
	responseModel := "provider-model-v2"
	mismatch := true
	prepared := prepareUsageLogInsert(&service.UsageLog{
		Model:                 "public-model",
		RequestedModel:        "public-model",
		UpstreamModel:         stringPtrForUsageLogInsert("provider-model-v1"),
		UpstreamResponseModel: &responseModel,
		UpstreamModelMismatch: &mismatch,
	})

	require.Len(t, prepared.args, len(usageLogInsertArgTypes))
	require.Equal(t, sql.NullString{String: "provider-model-v1", Valid: true}, prepared.args[6])
	require.Equal(t, sql.NullString{String: "provider-model-v2", Valid: true}, prepared.args[7])
	require.Equal(t, sql.NullBool{Bool: true, Valid: true}, prepared.args[8])
}

func TestUsageLogSelectColumnsIncludesUpstreamResponseAudit(t *testing.T) {
	require.Contains(t, usageLogSelectColumns, "upstream_response_model")
	require.Contains(t, usageLogSelectColumns, "upstream_model_mismatch")
}

// Keep the test independent from repository-wide pointer helpers.
func stringPtrForUsageLogInsert(value string) *string { return &value }
