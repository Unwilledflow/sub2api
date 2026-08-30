//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type externalMonitorRepoStub struct {
	ChannelMonitorRepository
	monitor     *ChannelMonitor
	publicCalls int
	updateCalls int
	updateErr   error
	rows        []*ChannelMonitorHistoryRow
	checkedAt   time.Time
}

func (r *externalMonitorRepoStub) ListPublic(context.Context) ([]*ChannelMonitor, error) {
	r.publicCalls++
	return []*ChannelMonitor{r.monitor}, nil
}

func (r *externalMonitorRepoStub) GetByID(context.Context, int64) (*ChannelMonitor, error) {
	return r.monitor, nil
}

func (r *externalMonitorRepoStub) Update(_ context.Context, monitor *ChannelMonitor) error {
	r.updateCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	r.monitor = monitor
	return nil
}

func (r *externalMonitorRepoStub) ListLatestForMonitorIDs(context.Context, []int64) (map[int64][]*ChannelMonitorLatest, error) {
	return map[int64][]*ChannelMonitorLatest{}, nil
}

func (r *externalMonitorRepoStub) ComputeAvailabilityForMonitors(context.Context, []int64, int) (map[int64][]*ChannelMonitorAvailability, error) {
	return map[int64][]*ChannelMonitorAvailability{}, nil
}

func (r *externalMonitorRepoStub) ListRecentHistoryForMonitors(context.Context, []int64, map[int64]string, int) (map[int64][]*ChannelMonitorHistoryEntry, error) {
	return map[int64][]*ChannelMonitorHistoryEntry{}, nil
}

func (r *externalMonitorRepoStub) InsertHistoryBatch(_ context.Context, rows []*ChannelMonitorHistoryRow) error {
	r.rows = rows
	return nil
}

func (r *externalMonitorRepoStub) MarkChecked(_ context.Context, _ int64, checkedAt time.Time) error {
	r.checkedAt = checkedAt
	return nil
}

func TestListUserViewIncludesDisabledPublicExternalMonitor(t *testing.T) {
	repo := &externalMonitorRepoStub{monitor: &ChannelMonitor{
		ID:             42,
		Name:           "channel-a",
		GroupName:      "Production GPT",
		PrimaryModel:   "gpt-5.4-mini",
		Enabled:        false,
		PublicVisible:  true,
		ManagementMode: ChannelMonitorManagementModeExternal,
	}}
	service := NewChannelMonitorService(repo, nil)

	views, err := service.ListUserView(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, repo.publicCalls)
	require.Len(t, views, 1)
	require.Equal(t, "Production GPT", views[0].GroupName)
}

func TestRecordExternalResultsPersistsActualModel(t *testing.T) {
	checkedAt := time.Date(2026, 7, 27, 0, 30, 0, 0, time.UTC)
	repo := &externalMonitorRepoStub{monitor: &ChannelMonitor{
		ID:             42,
		PrimaryModel:   "configured-model",
		ExtraModels:    []string{"actual-low-cost-model", "fallback-model"},
		ManagementMode: ChannelMonitorManagementModeExternal,
	}}
	service := NewChannelMonitorService(repo, nil)
	latency := 321

	err := service.RecordExternalResults(context.Background(), 42, []*CheckResult{{
		Model:     "actual-low-cost-model",
		Status:    MonitorStatusOperational,
		LatencyMs: &latency,
		Message:   "ok",
		CheckedAt: checkedAt,
	}})

	require.NoError(t, err)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, "actual-low-cost-model", repo.monitor.PrimaryModel)
	require.Equal(t, []string{"configured-model", "fallback-model"}, repo.monitor.ExtraModels)
	require.Len(t, repo.rows, 1)
	require.Equal(t, "actual-low-cost-model", repo.rows[0].Model)
	require.Equal(t, checkedAt, repo.checkedAt)
}

func TestRecordExternalResultsRejectsNativeMonitor(t *testing.T) {
	repo := &externalMonitorRepoStub{monitor: &ChannelMonitor{ID: 42}}
	service := NewChannelMonitorService(repo, nil)

	err := service.RecordExternalResults(context.Background(), 42, []*CheckResult{{
		Model: "gpt-5.4-mini", Status: MonitorStatusOperational, CheckedAt: time.Now(),
	}})

	require.ErrorIs(t, err, ErrChannelMonitorNotExternallyManaged)
}
