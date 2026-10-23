package service

import (
	"context"
	"testing"
	"time"
)

type qualityWorkspacePlanRepo struct {
	ScheduledTestPlanRepository
	plans       []*ScheduledTestPlan
	triggeredID int64
}

func (r *qualityWorkspacePlanRepo) ListAll(context.Context) ([]*ScheduledTestPlan, error) {
	return r.plans, nil
}

func (r *qualityWorkspacePlanRepo) TriggerNow(_ context.Context, id int64) error {
	r.triggeredID = id
	return nil
}

type qualityWorkspaceResultRepo struct {
	ScheduledTestResultRepository
	items []*QualityHistoryResult
}

func (r *qualityWorkspaceResultRepo) ListAll(_ context.Context, beforeID int64, limit int) ([]*QualityHistoryResult, error) {
	out := make([]*QualityHistoryResult, 0, len(r.items))
	for _, item := range r.items {
		if beforeID == 0 || item.ID < beforeID {
			out = append(out, item)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func TestListAllPlansAndTriggerNow(t *testing.T) {
	plan := &ScheduledTestPlan{ID: 9, AccountID: 12, AccountName: "grok-a"}
	repo := &qualityWorkspacePlanRepo{plans: []*ScheduledTestPlan{plan}}
	svc := NewScheduledTestService(repo, &qualityWorkspaceResultRepo{})

	listed, err := svc.ListAllPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].AccountName != "grok-a" {
		t.Fatalf("listed plans = %#v", listed)
	}
	if err := svc.TriggerNow(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if repo.triggeredID != 9 {
		t.Fatalf("triggered %d", repo.triggeredID)
	}
}

func TestListAllResultsCursor(t *testing.T) {
	items := make([]*QualityHistoryResult, 0, 102)
	now := time.Now()
	for i := 102; i >= 1; i-- {
		items = append(items, &QualityHistoryResult{
			ScheduledTestResult: ScheduledTestResult{ID: int64(i), PlanID: 1, Status: "success", StartedAt: now, FinishedAt: now, CreatedAt: now},
			AccountID:           12,
			AccountName:         "grok-a",
		})
	}
	svc := NewScheduledTestService(&qualityWorkspacePlanRepo{}, &qualityWorkspaceResultRepo{items: items})
	page, err := svc.ListAllResults(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 100 || page.NextCursor != 3 {
		t.Fatalf("page=%#v", page)
	}
}
