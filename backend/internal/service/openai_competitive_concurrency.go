package service

import (
	"sync"
	"time"
)

const (
	competitiveSlowThreshold = 3
	competitiveSlowWindow    = 10 * time.Minute
)

type competitiveSlowKey struct {
	accountID        int64
	model            string
	performanceClass string
}

type competitiveSlowState struct {
	mu          sync.Mutex
	consecutive int
	updatedAt   time.Time
}

var competitiveSlowAccounts sync.Map

// ReportOpenAICompetitiveRaceOutcome delays scheduler penalties until an
// account has missed the full first-output window repeatedly. Any successful
// output clears the streak and feeds the normal runtime scheduler immediately.
func (s *OpenAIGatewayService) ReportOpenAICompetitiveRaceOutcome(
	accountID int64,
	model string,
	performanceClass string,
	success bool,
	firstTokenMs *int,
	fullWindowTimeout bool,
) {
	if s == nil || accountID <= 0 {
		return
	}
	key := competitiveSlowKey{accountID: accountID, model: model, performanceClass: performanceClass}
	if success {
		competitiveSlowAccounts.Delete(key)
		return
	}
	if !fullWindowTimeout {
		return
	}
	value, _ := competitiveSlowAccounts.LoadOrStore(key, &competitiveSlowState{})
	state, _ := value.(*competitiveSlowState)
	if state == nil {
		return
	}
	now := time.Now()
	state.mu.Lock()
	if state.updatedAt.IsZero() || now.Sub(state.updatedAt) > competitiveSlowWindow {
		state.consecutive = 0
	}
	state.consecutive++
	state.updatedAt = now
	consecutive := state.consecutive
	if consecutive >= competitiveSlowThreshold {
		state.consecutive = 0
	}
	state.mu.Unlock()
	if consecutive < competitiveSlowThreshold {
		return
	}
	timeoutMs := int((3 * time.Second) / time.Millisecond)
	s.ReportOpenAIAccountModelScheduleResultWithClass(accountID, model, performanceClass, true, &timeoutMs)
}
