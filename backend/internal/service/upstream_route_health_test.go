package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamRouteHealthSuppressesOnlyAfterFullFailureAndRecoversWithHysteresis(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	health := newUpstreamRouteHealth(2, 2, time.Minute)
	health.now = func() time.Time { return now }

	monitor := &ChannelMonitor{ID: 41, GroupName: "route:relay", Enabled: true}
	account := &Account{Extra: map[string]any{"routing_channel": "relay"}}
	require.Equal(t, UpstreamRouteKeyForMonitor(monitor), UpstreamRouteKeyForAccount(account))
	health.ConfigureChannelMonitor(monitor)
	require.True(t, health.AllowsAccount(account))

	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "failed", FailureScope: MonitorFailureScopeRoute}})
	require.True(t, health.AllowsAccount(account), "one failed monitor cycle must not move traffic")

	now = now.Add(time.Second)
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "error", FailureScope: MonitorFailureScopeRoute}})
	require.False(t, health.AllowsAccount(account))
	status, ok := health.Status(UpstreamRouteKeyForAccount(account))
	require.True(t, ok)
	require.Equal(t, 2, status.ConsecutiveFailures)
	require.True(t, status.Open)
	require.Equal(t, monitor.ID, status.MonitorID)

	now = now.Add(time.Second)
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "operational"}})
	status, ok = health.Status(UpstreamRouteKeyForAccount(account))
	require.True(t, ok)
	require.Zero(t, status.ConsecutiveSuccesses, "cooldown samples must not reopen a failed route")
	require.False(t, health.AllowsAccount(account))

	now = now.Add(2 * time.Minute)
	require.False(t, health.AllowsAccount(account), "route must not reopen on a timer without healthy monitor evidence")
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "operational"}})
	require.False(t, health.AllowsAccount(account), "one success must not flap the route back into service")
	now = now.Add(time.Second)
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "operational"}})
	require.True(t, health.AllowsAccount(account))
}

func TestUpstreamRouteHealthIgnoresPartialModelFailure(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	monitor := &ChannelMonitor{ID: 42, GroupName: "route:relay", Enabled: true}
	account := &Account{Extra: map[string]any{"routing_channel": "relay"}}
	health.ConfigureChannelMonitor(monitor)

	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{
		{Model: "fable5", Status: "failed"},
		{Model: "opus4.8", Status: "operational"},
	})
	require.True(t, health.AllowsAccount(account))

	status, ok := health.Status(UpstreamRouteKeyForAccount(account))
	require.True(t, ok)
	require.Equal(t, "degraded", status.LastStatus)
	require.Zero(t, status.ConsecutiveFailures)
}

func TestUpstreamRouteHealthUsesExplicitRouteKeyAndSchedulerFiltersAtReadTime(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	monitor := &ChannelMonitor{ID: 8, GroupName: "route:high-capacity", Enabled: true}
	blocked := Account{ID: 11, Extra: map[string]any{"routing_channel": "high-capacity"}}
	healthy := Account{ID: 12, Extra: map[string]any{"routing_channel": "secondary"}}

	health.ConfigureChannelMonitor(monitor)
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "failed", FailureScope: MonitorFailureScopeRoute}})
	require.False(t, health.AllowsAccount(&blocked))
	require.True(t, health.AllowsAccount(&healthy))

	snapshot := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	snapshot.SetUpstreamRouteHealth(health)
	filtered := snapshot.filterAccountsByUpstreamRouteHealth([]Account{blocked, healthy})
	require.Len(t, filtered, 1)
	require.Equal(t, healthy.ID, filtered[0].ID)
}

func TestChannelMonitorResultObserverFeedsRouteHealth(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	svc := &ChannelMonitorService{}
	svc.SetResultObserver(health)
	monitor := &ChannelMonitor{ID: 9, GroupName: "route:relay", Enabled: true}
	account := &Account{Extra: map[string]any{"routing_channel": "relay"}}

	health.ConfigureChannelMonitor(monitor)
	svc.notifyResultObserver(context.Background(), monitor, []*CheckResult{{Status: "failed"}})
	require.True(t, health.AllowsAccount(account), "model/request failures must not suppress a route")
}

func TestGatewayProtocolsFilterAlertedRoutesIncludingStickyHydration(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	monitor := &ChannelMonitor{ID: 10, GroupName: "route:relay", Enabled: true}
	blocked := Account{ID: 11, Extra: map[string]any{"routing_channel": "relay"}}
	health.ConfigureChannelMonitor(monitor)
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{Status: "failed", FailureScope: MonitorFailureScopeRoute}})

	claudeGateway := &GatewayService{}
	claudeGateway.SetUpstreamRouteHealth(health)
	openAIGateway := &OpenAIGatewayService{}
	openAIGateway.SetUpstreamRouteHealth(health)

	require.Empty(t, claudeGateway.filterAccountsByUpstreamRouteHealth([]Account{blocked}))
	require.Empty(t, openAIGateway.filterAccountsByUpstreamRouteHealth([]Account{blocked}))

	claudeHydrated, err := claudeGateway.hydrateSelectedAccount(context.Background(), &blocked)
	require.NoError(t, err)
	require.Nil(t, claudeHydrated)
	openAIHydrated, err := openAIGateway.hydrateSelectedAccount(context.Background(), &blocked)
	require.NoError(t, err)
	require.Nil(t, openAIHydrated)
}

func TestUpstreamRouteHealthRequiresOptInAndExplicitBinding(t *testing.T) {
	disabled := newUpstreamRouteHealthWithEnabled(1, 1, time.Minute, false)
	monitor := &ChannelMonitor{ID: 20, GroupName: "route:relay", Enabled: true}
	account := &Account{Extra: map[string]any{"routing_channel": "relay"}}
	disabled.ConfigureChannelMonitor(monitor)
	disabled.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.True(t, disabled.AllowsAccount(account))

	health := newUpstreamRouteHealth(1, 1, time.Minute)
	implicitMonitor := &ChannelMonitor{ID: 21, Endpoint: "https://relay.example/v1", Enabled: true}
	implicitAccount := &Account{Credentials: map[string]any{"base_url": "https://relay.example/v1"}}
	require.Empty(t, UpstreamRouteKeyForMonitor(implicitMonitor))
	require.Empty(t, UpstreamRouteKeyForAccount(implicitAccount))
	health.ConfigureChannelMonitor(implicitMonitor)
	health.ObserveChannelMonitorResults(context.Background(), implicitMonitor, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.True(t, health.AllowsAccount(implicitAccount))
}

func TestUpstreamRouteHealthDropsStaleDisabledAndRemovedMonitorResults(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	oldConfig := &ChannelMonitor{
		ID: 30, GroupName: "route:relay", Enabled: true, APIKey: "old-key", PrimaryModel: "model-a",
	}
	account := &Account{Extra: map[string]any{"routing_channel": "relay"}}
	health.ConfigureChannelMonitor(oldConfig)

	newConfig := *oldConfig
	newConfig.APIKey = "new-key"
	health.ConfigureChannelMonitor(&newConfig)
	health.ObserveChannelMonitorResults(context.Background(), oldConfig, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.True(t, health.AllowsAccount(account), "late results from an old configuration must be fenced")

	health.ObserveChannelMonitorResults(context.Background(), &newConfig, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.False(t, health.AllowsAccount(account))

	disabled := newConfig
	disabled.Enabled = false
	health.ConfigureChannelMonitor(&disabled)
	require.True(t, health.AllowsAccount(account))
	health.ObserveChannelMonitorResults(context.Background(), &newConfig, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.True(t, health.AllowsAccount(account))

	health.ConfigureChannelMonitor(&newConfig)
	health.RemoveChannelMonitor(newConfig.ID)
	health.ObserveChannelMonitorResults(context.Background(), &newConfig, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.True(t, health.AllowsAccount(account))
}

func TestUpstreamRouteHealthFailsOpenForDuplicateRouteOwners(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	first := &ChannelMonitor{ID: 40, GroupName: "route:relay", Enabled: true}
	second := &ChannelMonitor{ID: 41, GroupName: "route:relay", Enabled: true}
	account := &Account{Extra: map[string]any{"routing_channel": "relay"}}
	health.ConfigureChannelMonitor(first)
	health.ConfigureChannelMonitor(second)
	health.ObserveChannelMonitorResults(context.Background(), first, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	health.ObserveChannelMonitorResults(context.Background(), second, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})
	require.True(t, health.AllowsAccount(account), "ambiguous route ownership must not suppress traffic")
}

func TestAcquiredSelectionReleasesSlotWhenRouteClosesBeforeHydration(t *testing.T) {
	health := newUpstreamRouteHealth(1, 1, time.Minute)
	monitor := &ChannelMonitor{ID: 50, GroupName: "route:relay", Enabled: true}
	account := &Account{ID: 51, Extra: map[string]any{"routing_channel": "relay"}}
	health.ConfigureChannelMonitor(monitor)
	health.ObserveChannelMonitorResults(context.Background(), monitor, []*CheckResult{{
		Status: "error", FailureScope: MonitorFailureScopeRoute,
	}})

	claudeReleases := 0
	claudeGateway := &GatewayService{}
	claudeGateway.SetUpstreamRouteHealth(health)
	selection, err := claudeGateway.newAcquiredSelectionResult(context.Background(), account, func() { claudeReleases++ })
	require.Error(t, err)
	require.Nil(t, selection)
	require.Equal(t, 1, claudeReleases)

	openAIReleases := 0
	openAIGateway := &OpenAIGatewayService{}
	openAIGateway.SetUpstreamRouteHealth(health)
	selection, err = openAIGateway.newAcquiredSelectionResult(context.Background(), account, func() { openAIReleases++ })
	require.Error(t, err)
	require.Nil(t, selection)
	require.Equal(t, 1, openAIReleases)
}
