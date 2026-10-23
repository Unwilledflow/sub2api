package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestListGroupBucketsPassesGroupIDsAsPQArray(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ids := []int64{17, 142, 152}
	since := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	bucket := sqlmock.NewRows([]string{"group_id", "bucket_start", "checked", "degraded", "failed", "unknown"}).
		AddRow(int64(17), since, 4, 1, 2, 1).
		AddRow(int64(142), since, 2, 0, 0, 0)
	mock.ExpectQuery("ag.group_id = ANY").
		WithArgs(pq.Array(ids), since, int64(300)).
		WillReturnRows(bucket)

	repo := &groupQualityCheckRepository{db: db}
	rows, err := repo.ListGroupBuckets(context.Background(), ids, since, 300)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, int64(17), rows[0].GroupID)
	require.Equal(t, 4, rows[0].Checked)
	require.Equal(t, 1, rows[0].Degraded)
	require.Equal(t, 2, rows[0].Failed)
	require.Equal(t, 1, rows[0].Unknown)
	require.NoError(t, mock.ExpectationsWereMet())
}
