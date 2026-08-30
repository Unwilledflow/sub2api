package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultUpstreamRouteFailureThreshold  = 2
	defaultUpstreamRouteRecoveryThreshold = 2
	defaultUpstreamRouteCooldown          = 90 * time.Second
)

// UpstreamRouteHealth is a monitor-fed overlay for the existing account
// scheduler. It intentionally does not mutate account state or scheduler
// snapshots: an alert can take effect immediately and recovery reveals the
// original candidates without a database or Redis rebuild.
type UpstreamRouteHealth struct {
	writeMu  sync.Mutex
	snapshot atomic.Pointer[upstreamRouteHealthSnapshot]
	enabled  atomic.Bool
	now      func() time.Time

	monitorConfigs map[int64]upstreamRouteMonitorConfig
	monitorStates  map[int64]upstreamRouteHealthState

	failureThreshold  int
	recoveryThreshold int
	cooldown          time.Duration
}

type upstreamRouteHealthSnapshot struct {
	routes map[string]upstreamRouteHealthState
}

type upstreamRouteMonitorConfig struct {
	RouteKey    string
	Fingerprint string
}

type upstreamRouteHealthState struct {
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	Open                 bool
	SuppressedUntil      time.Time
	LastObservedAt       time.Time
	LastStatus           string
	MonitorID            int64
}

// UpstreamRouteHealthStatus is an immutable diagnostic copy. It is useful for
// operations views without exposing the mutable scheduler overlay.
type UpstreamRouteHealthStatus struct {
	RouteKey             string
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	Open                 bool
	SuppressedUntil      time.Time
	LastObservedAt       time.Time
	LastStatus           string
	MonitorID            int64
}

func NewUpstreamRouteHealth(enabled bool) *UpstreamRouteHealth {
	return newUpstreamRouteHealthWithEnabled(
		defaultUpstreamRouteFailureThreshold,
		defaultUpstreamRouteRecoveryThreshold,
		defaultUpstreamRouteCooldown,
		enabled,
	)
}

func newUpstreamRouteHealth(failureThreshold, recoveryThreshold int, cooldown time.Duration) *UpstreamRouteHealth {
	return newUpstreamRouteHealthWithEnabled(failureThreshold, recoveryThreshold, cooldown, true)
}

func newUpstreamRouteHealthWithEnabled(failureThreshold, recoveryThreshold int, cooldown time.Duration, enabled bool) *UpstreamRouteHealth {
	if failureThreshold < 1 {
		failureThreshold = defaultUpstreamRouteFailureThreshold
	}
	if recoveryThreshold < 1 {
		recoveryThreshold = defaultUpstreamRouteRecoveryThreshold
	}
	if cooldown <= 0 {
		cooldown = defaultUpstreamRouteCooldown
	}
	h := &UpstreamRouteHealth{
		now:               time.Now,
		monitorConfigs:    make(map[int64]upstreamRouteMonitorConfig),
		monitorStates:     make(map[int64]upstreamRouteHealthState),
		failureThreshold:  failureThreshold,
		recoveryThreshold: recoveryThreshold,
		cooldown:          cooldown,
	}
	h.enabled.Store(enabled)
	h.snapshot.Store(&upstreamRouteHealthSnapshot{routes: make(map[string]upstreamRouteHealthState)})
	return h
}

func (h *UpstreamRouteHealth) Enabled() bool {
	return h != nil && h.enabled.Load()
}

// ConfigureChannelMonitor establishes the generation fence for future results.
// A route with multiple enabled monitors is deliberately fail-open because the
// first release has no deterministic way to aggregate conflicting monitors.
func (h *UpstreamRouteHealth) ConfigureChannelMonitor(monitor *ChannelMonitor) {
	if h == nil || monitor == nil {
		return
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	delete(h.monitorConfigs, monitor.ID)
	delete(h.monitorStates, monitor.ID)
	if h.Enabled() && monitor.Enabled {
		if routeKey := UpstreamRouteKeyForMonitor(monitor); routeKey != "" {
			h.monitorConfigs[monitor.ID] = upstreamRouteMonitorConfig{
				RouteKey:    routeKey,
				Fingerprint: channelMonitorRoutingFingerprint(monitor),
			}
		}
	}
	h.publishSnapshotLocked()
}

func (h *UpstreamRouteHealth) RemoveChannelMonitor(monitorID int64) {
	if h == nil {
		return
	}
	h.writeMu.Lock()
	delete(h.monitorConfigs, monitorID)
	delete(h.monitorStates, monitorID)
	h.publishSnapshotLocked()
	h.writeMu.Unlock()
}

// ObserveChannelMonitorResults consumes one completed monitor cycle. Only a
// cycle in which every checked model fails trips the route breaker; a single
// model failure remains a model-capability concern and must not blackhole a
// healthy route for other models.
func (h *UpstreamRouteHealth) ObserveChannelMonitorResults(ctx context.Context, monitor *ChannelMonitor, results []*CheckResult) {
	if !h.Enabled() || monitor == nil || !monitor.Enabled || ctx.Err() != nil {
		return
	}
	routeKey := UpstreamRouteKeyForMonitor(monitor)
	if routeKey == "" {
		return
	}

	valid, routeFailures, operational := 0, 0, 0
	for _, result := range results {
		if result == nil {
			continue
		}
		valid++
		switch strings.ToLower(strings.TrimSpace(result.Status)) {
		case "operational":
			operational++
		}
		if result.FailureScope == MonitorFailureScopeRoute {
			routeFailures++
		}
	}
	if valid == 0 {
		return
	}

	if routeFailures == valid {
		h.update(monitor, routeKey, "failed", true, false)
		return
	}
	if operational == valid {
		h.update(monitor, routeKey, "operational", false, true)
		return
	}
	// A mixed/degraded sample is recorded but deliberately does not trigger a
	// whole-route switch. It also breaks a streak so stale failures cannot trip
	// the breaker after intervening partial success.
	h.update(monitor, routeKey, "degraded", false, false)
}

func (h *UpstreamRouteHealth) update(monitor *ChannelMonitor, routeKey, status string, failed, succeeded bool) {
	if h == nil || monitor == nil || routeKey == "" {
		return
	}
	now := h.now()
	h.writeMu.Lock()
	defer h.writeMu.Unlock()

	config, configured := h.monitorConfigs[monitor.ID]
	if !configured || config.RouteKey != routeKey || config.Fingerprint != channelMonitorRoutingFingerprint(monitor) {
		return
	}
	state := h.monitorStates[monitor.ID]
	state.LastObservedAt = now
	state.LastStatus = status
	state.MonitorID = monitor.ID

	switch {
	case failed:
		state.ConsecutiveFailures++
		state.ConsecutiveSuccesses = 0
		if state.ConsecutiveFailures >= h.failureThreshold {
			state.Open = true
			state.SuppressedUntil = now.Add(h.cooldown)
		}
	case succeeded:
		state.ConsecutiveFailures = 0
		// A route stays out of rotation for the full cooldown. Successful
		// monitor cycles during that window are useful diagnostics, but they
		// do not count toward recovery; otherwise a brief blip can immediately
		// reintroduce the same route to every scheduler.
		if state.Open && state.SuppressedUntil.After(now) {
			state.ConsecutiveSuccesses = 0
			break
		}
		state.ConsecutiveSuccesses++
		if state.ConsecutiveSuccesses >= h.recoveryThreshold {
			state.Open = false
			state.SuppressedUntil = time.Time{}
		}
	default:
		state.ConsecutiveFailures = 0
		state.ConsecutiveSuccesses = 0
	}
	h.monitorStates[monitor.ID] = state
	h.publishSnapshotLocked()
}

func (h *UpstreamRouteHealth) publishSnapshotLocked() {
	owners := make(map[string]int64, len(h.monitorConfigs))
	conflicts := make(map[string]struct{})
	for monitorID, config := range h.monitorConfigs {
		if existing, ok := owners[config.RouteKey]; ok && existing != monitorID {
			conflicts[config.RouteKey] = struct{}{}
			continue
		}
		owners[config.RouteKey] = monitorID
	}

	routes := make(map[string]upstreamRouteHealthState, len(owners))
	for routeKey, monitorID := range owners {
		if _, conflict := conflicts[routeKey]; conflict {
			continue
		}
		if state, ok := h.monitorStates[monitorID]; ok {
			routes[routeKey] = state
		}
	}
	h.snapshot.Store(&upstreamRouteHealthSnapshot{routes: routes})
}

// AllowsAccount is the scheduling hot-path predicate. A route stays blocked
// until both its cooldown and recovery-success threshold have elapsed; it
// never silently re-enters rotation just because a timer expires. The method
// performs one atomic snapshot read and one map lookup, independent from
// database, Redis, and monitor storage availability.
func (h *UpstreamRouteHealth) AllowsAccount(account *Account) bool {
	if h == nil || account == nil {
		return true
	}
	routeKey := UpstreamRouteKeyForAccount(account)
	if routeKey == "" {
		return true
	}
	snapshot := h.snapshot.Load()
	if snapshot == nil {
		return true
	}
	state, found := snapshot.routes[routeKey]
	return !found || !state.Open
}

// FilterAccounts keeps account snapshots immutable and only allocates when a
// currently suppressed upstream route is present.
func (h *UpstreamRouteHealth) FilterAccounts(accounts []Account) []Account {
	if h == nil || len(accounts) == 0 {
		return accounts
	}
	for i := range accounts {
		if !h.AllowsAccount(&accounts[i]) {
			filtered := make([]Account, 0, len(accounts)-1)
			for j := range accounts {
				if h.AllowsAccount(&accounts[j]) {
					filtered = append(filtered, accounts[j])
				}
			}
			return filtered
		}
	}
	return accounts
}

func (h *UpstreamRouteHealth) Status(routeKey string) (UpstreamRouteHealthStatus, bool) {
	if h == nil {
		return UpstreamRouteHealthStatus{}, false
	}
	routeKey = strings.TrimSpace(routeKey)
	snapshot := h.snapshot.Load()
	if routeKey == "" || snapshot == nil {
		return UpstreamRouteHealthStatus{}, false
	}
	state, ok := snapshot.routes[routeKey]
	if !ok {
		return UpstreamRouteHealthStatus{}, false
	}
	return UpstreamRouteHealthStatus{
		RouteKey:             routeKey,
		ConsecutiveFailures:  state.ConsecutiveFailures,
		ConsecutiveSuccesses: state.ConsecutiveSuccesses,
		Open:                 state.Open,
		SuppressedUntil:      state.SuppressedUntil,
		LastObservedAt:       state.LastObservedAt,
		LastStatus:           state.LastStatus,
		MonitorID:            state.MonitorID,
	}, true
}

// UpstreamRouteKeyForMonitor accepts only explicit route identities. Endpoint
// inference is intentionally excluded because several unrelated accounts can
// legitimately share one provider base URL.
func UpstreamRouteKeyForMonitor(monitor *ChannelMonitor) string {
	if monitor == nil {
		return ""
	}
	return explicitUpstreamRouteKey(monitor.GroupName)
}

// UpstreamRouteKeyForAccount accepts only extra.routing_channel. Credentials
// and endpoint URLs are not routing-control metadata.
func UpstreamRouteKeyForAccount(account *Account) string {
	if account == nil {
		return ""
	}
	value := strings.TrimSpace(account.GetExtraString("routing_channel"))
	if strings.HasPrefix(strings.ToLower(value), "route:") {
		return explicitUpstreamRouteKey(value)
	}
	return explicitUpstreamRouteKey("route:" + value)
}

func explicitUpstreamRouteKey(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < len("route:") || !strings.EqualFold(value[:len("route:")], "route:") {
		return ""
	}
	value = strings.ToLower(strings.TrimSpace(value[len("route:"):]))
	if value == "" {
		return ""
	}
	return "route:" + value
}

func channelMonitorRoutingFingerprint(monitor *ChannelMonitor) string {
	if monitor == nil {
		return ""
	}
	payload, err := json.Marshal(struct {
		Provider         string
		APIMode          string
		Endpoint         string
		APIKey           string
		PrimaryModel     string
		ExtraModels      []string
		GroupName        string
		ExtraHeaders     map[string]string
		BodyOverrideMode string
		BodyOverride     map[string]any
	}{
		Provider:         monitor.Provider,
		APIMode:          monitor.APIMode,
		Endpoint:         monitor.Endpoint,
		APIKey:           monitor.APIKey,
		PrimaryModel:     monitor.PrimaryModel,
		ExtraModels:      monitor.ExtraModels,
		GroupName:        monitor.GroupName,
		ExtraHeaders:     monitor.ExtraHeaders,
		BodyOverrideMode: monitor.BodyOverrideMode,
		BodyOverride:     monitor.BodyOverride,
	})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(payload)
	return string(digest[:])
}
