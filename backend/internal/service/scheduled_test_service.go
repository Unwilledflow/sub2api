package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := normalizeScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// ListAllPlans returns every scheduled test plan joined with its account name.
func (s *ScheduledTestService) ListAllPlans(ctx context.Context) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListAll(ctx)
}

// TriggerNow queues an enabled plan to run on the next runner tick.
func (s *ScheduledTestService) TriggerNow(ctx context.Context, id int64) error {
	return s.planRepo.TriggerNow(ctx, id)
}

// ListAllResults returns cursor-paginated scheduled-test history across accounts.
func (s *ScheduledTestService) ListAllResults(ctx context.Context, beforeID int64) (*QualityHistoryPage, error) {
	items, err := s.resultRepo.ListAll(ctx, beforeID, 101)
	if err != nil {
		return nil, err
	}
	page := &QualityHistoryPage{Items: items}
	if len(items) > 100 {
		page.Items = items[:100]
		page.NextCursor = items[99].ID
	}
	return page, nil
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := normalizeScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

func normalizeScheduledTestPlan(plan *ScheduledTestPlan) error {
	if plan == nil {
		return fmt.Errorf("scheduled test plan is required")
	}
	if err := normalizeScheduledQualityProvider(plan); err != nil {
		return err
	}
	plan.PromptText = strings.TrimSpace(plan.PromptText)
	if plan.PromptText == "" {
		plan.PromptText = DefaultScheduledTestPrompt
	}
	if len([]byte(plan.PromptText)) > maxScheduledTestPromptSize {
		return fmt.Errorf("scheduled test prompt is too long")
	}
	plan.CronExpression = strings.TrimSpace(plan.CronExpression)
	if plan.CronExpression == "" {
		plan.CronExpression = DefaultScheduledTestCron
	}
	return nil
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, min(limit, 200))
}

// Polling reads only newly persisted results, avoiding repeated transfer of
// large HTML/artwork and audit evidence. Authorization remains in the handler.
func (s *ScheduledTestService) ListResultsAfter(ctx context.Context, planID int64, limit int, afterID int64) ([]*ScheduledTestResult, error) {
	if afterID <= 0 {
		return s.ListResults(ctx, planID, limit)
	}
	if limit <= 0 {
		limit = 50
	}
	if repo, ok := s.resultRepo.(interface {
		ListByPlanIDAfter(context.Context, int64, int, int64) ([]*ScheduledTestResult, error)
	}); ok {
		return repo.ListByPlanIDAfter(ctx, planID, min(limit, 200), afterID)
	}
	results, err := s.ListResults(ctx, planID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*ScheduledTestResult, 0, len(results))
	for _, result := range results {
		if result != nil && result.ID > afterID {
			out = append(out, result)
		}
	}
	return out, nil
}

// SaveResult inserts a result and keeps at least the two records needed for
// consecutive-quality decisions, even when the display retention is one.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	if result == nil {
		return fmt.Errorf("scheduled test result is required")
	}
	result.ID = 0
	result.PlanID = planID
	saved, err := s.resultRepo.Create(ctx, result)
	if err != nil {
		return err
	}
	if saved == nil || saved.ID <= 0 || saved.PlanID != planID {
		return fmt.Errorf("scheduled test result was not persisted")
	}
	*result = *saved
	return s.resultRepo.PruneOldResults(ctx, planID, max(maxResults, 2))
}

func (s *ScheduledTestService) hasConsecutiveDegradedResults(ctx context.Context, current *ScheduledTestResult) (bool, error) {
	if current == nil || current.ID <= 0 || current.Status != "degraded" {
		return false, nil
	}
	results, err := s.resultRepo.ListByPlanID(ctx, current.PlanID, 2)
	if err != nil {
		return false, err
	}
	if len(results) < 2 || results[0] == nil || results[1] == nil {
		return false, nil
	}
	latest, previous := results[0], results[1]
	return latest.ID == current.ID && previous.ID > 0 && previous.ID != latest.ID &&
		latest.PlanID == current.PlanID && previous.PlanID == current.PlanID &&
		latest.Status == "degraded" && previous.Status == "degraded", nil
}

// Legacy quality pauses share the temporary-ban fields with other subsystems.
// Clearing them requires a compare-and-clear rather than an unconditional reset.
type scheduledQualityPauseRepository interface {
	ClearScheduledQualityPause(context.Context, int64, string) (bool, error)
}

func (s *ScheduledTestService) clearScheduledQualityPause(ctx context.Context, accountID int64, reason string) (bool, error) {
	repo, ok := s.resultRepo.(scheduledQualityPauseRepository)
	if !ok {
		return false, fmt.Errorf("conditional scheduled quality recovery is unavailable")
	}
	return repo.ClearScheduledQualityPause(ctx, accountID, reason)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	// A schedule with no future occurrence (e.g. a leap-day cron) has no zero
	// value to fall back on: cron's Next returns the zero time, and a zero
	// next_run_at is "due" forever for the runner.
	next := sched.Next(from)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("schedule has no future run time")
	}
	return next, nil
}
