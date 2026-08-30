//go:build unit

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorExternalMetadataNeverLeaksIntoRuntimeHeaders(t *testing.T) {
	row := &ent.ChannelMonitor{ExtraHeaders: map[string]string{
		"X-Client": "ops",
		service.ChannelMonitorExternalRefMetadataKey:    "ops:1:account:17",
		service.ChannelMonitorPublicVisibleMetadataKey:  "true",
		service.ChannelMonitorManagementModeMetadataKey: service.ChannelMonitorManagementModeExternal,
	}}

	monitor := entToServiceMonitor(row)

	require.Equal(t, "ops:1:account:17", monitor.ExternalRef)
	require.True(t, monitor.PublicVisible)
	require.Equal(t, service.ChannelMonitorManagementModeExternal, monitor.ManagementMode)
	require.Equal(t, map[string]string{"X-Client": "ops"}, monitor.ExtraHeaders)

	persisted := channelMonitorHeadersForPersistence(monitor)
	require.Equal(t, "true", persisted[service.ChannelMonitorPublicVisibleMetadataKey])
	require.Equal(t, "ops", persisted["X-Client"])
}
