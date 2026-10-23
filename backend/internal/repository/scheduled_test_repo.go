package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// --- Plan Repository ---

type scheduledTestPlanRepository struct {
	db *sql.DB
}

func NewScheduledTestPlanRepository(db *sql.DB) service.ScheduledTestPlanRepository {
	return &scheduledTestPlanRepository{db: db}
}

func (r *scheduledTestPlanRepository) Create(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	config, err := json.Marshal(plan.QualityConfig)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_plans (account_id, model_id, prompt_text, cron_expression, enabled, max_results, auto_recover, quality_check_enabled, quality_provider, quality_config, next_run_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		RETURNING id, account_id, model_id, prompt_text, cron_expression, enabled, max_results, auto_recover, quality_check_enabled, quality_provider, quality_config, quality_audit_state, last_run_at, next_run_at, created_at, updated_at
	`, plan.AccountID, plan.ModelID, plan.PromptText, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.QualityCheckEnabled, plan.QualityProvider, string(config), plan.NextRunAt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, model_id, prompt_text, cron_expression, enabled, max_results, auto_recover, quality_check_enabled, quality_provider, quality_config, quality_audit_state, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans WHERE id = $1
	`, id)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, model_id, prompt_text, cron_expression, enabled, max_results, auto_recover, quality_check_enabled, quality_provider, quality_config, quality_audit_state, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans WHERE account_id = $1
		ORDER BY created_at DESC, id DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlans(rows)
}

func (r *scheduledTestPlanRepository) ListDue(ctx context.Context, now time.Time) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, model_id, prompt_text, cron_expression, enabled, max_results, auto_recover, quality_check_enabled, quality_provider, quality_config, quality_audit_state, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans
		WHERE enabled = true AND next_run_at <= $1
		ORDER BY next_run_at ASC, id ASC
	`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlans(rows)
}

func (r *scheduledTestPlanRepository) ListAll(ctx context.Context) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.account_id, p.model_id, p.prompt_text, p.cron_expression, p.enabled, p.max_results, p.auto_recover, p.quality_check_enabled,
			p.quality_provider, p.last_run_at, p.next_run_at, p.created_at, p.updated_at, a.name
		FROM scheduled_test_plans p
		JOIN accounts a ON a.id = p.account_id
		WHERE a.deleted_at IS NULL
		ORDER BY p.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlansWithAccountName(rows)
}

func (r *scheduledTestPlanRepository) TriggerNow(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_plans
		SET next_run_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND enabled = true
	`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("plan must be enabled")
	}
	return nil
}

func (r *scheduledTestPlanRepository) Update(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	config, err := json.Marshal(plan.QualityConfig)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		UPDATE scheduled_test_plans
		SET model_id = $2, prompt_text = $3, cron_expression = $4, enabled = $5, max_results = $6, auto_recover = $7, quality_check_enabled = $8, quality_provider = $9, quality_config = $10, next_run_at = $11, updated_at = NOW()
		WHERE id = $1
		RETURNING id, account_id, model_id, prompt_text, cron_expression, enabled, max_results, auto_recover, quality_check_enabled, quality_provider, quality_config, quality_audit_state, last_run_at, next_run_at, created_at, updated_at
	`, plan.ID, plan.ModelID, plan.PromptText, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.QualityCheckEnabled, plan.QualityProvider, string(config), plan.NextRunAt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id = $1`, id)
	return err
}

func (r *scheduledTestPlanRepository) UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_plans SET last_run_at = $2, next_run_at = $3, updated_at = NOW() WHERE id = $1
	`, id, lastRunAt, nextRunAt)
	return err
}

// --- Result Repository ---

type scheduledTestResultRepository struct {
	db *sql.DB
}

func NewScheduledTestResultRepository(db *sql.DB) service.ScheduledTestResultRepository {
	return &scheduledTestResultRepository{db: db}
}

func (r *scheduledTestResultRepository) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_results (plan_id, status, response_text, error_message, latency_ms, started_at, finished_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		RETURNING id, plan_id, status, response_text, error_message, latency_ms, started_at, finished_at, created_at
	`, result.PlanID, result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt)

	out := &service.ScheduledTestResult{}
	if err := row.Scan(
		&out.ID, &out.PlanID, &out.Status, &out.ResponseText, &out.ErrorMessage,
		&out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *scheduledTestResultRepository) ListAll(ctx context.Context, beforeID int64, limit int) ([]*service.QualityHistoryResult, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.plan_id, r.status, r.response_text, r.error_message, r.latency_ms, r.started_at, r.finished_at, r.created_at,
			p.account_id, a.name
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		JOIN accounts a ON a.id = p.account_id
		WHERE a.deleted_at IS NULL AND ($1::bigint = 0 OR r.id < $1)
		ORDER BY r.id DESC
		LIMIT $2
	`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]*service.QualityHistoryResult, 0)
	for rows.Next() {
		item := &service.QualityHistoryResult{}
		if err := rows.Scan(
			&item.ID, &item.PlanID, &item.Status, &item.ResponseText, &item.ErrorMessage,
			&item.LatencyMs, &item.StartedAt, &item.FinishedAt, &item.CreatedAt,
			&item.AccountID, &item.AccountName,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *scheduledTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.ScheduledTestResult, error) {
	return r.ListByPlanIDAfter(ctx, planID, limit, 0)
}

func (r *scheduledTestResultRepository) ListByPlanIDAfter(ctx context.Context, planID int64, limit int, afterID int64) ([]*service.ScheduledTestResult, error) {
	filter := ""
	args := []any{planID, limit}
	if afterID > 0 {
		filter = " AND id > $3"
		args = append(args, afterID)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, plan_id, status, response_text, error_message, latency_ms, started_at, finished_at, created_at
		FROM scheduled_test_results
		WHERE plan_id = $1`+filter+`
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*service.ScheduledTestResult
	for rows.Next() {
		r := &service.ScheduledTestResult{}
		if err := rows.Scan(
			&r.ID, &r.PlanID, &r.Status, &r.ResponseText, &r.ErrorMessage,
			&r.LatencyMs, &r.StartedAt, &r.FinishedAt, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (r *scheduledTestResultRepository) PruneOldResults(ctx context.Context, planID int64, keepCount int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM scheduled_test_results
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY plan_id ORDER BY created_at DESC, id DESC) AS rn
				FROM scheduled_test_results
				WHERE plan_id = $1
			) ranked
			WHERE rn > $2
		)
	`, planID, keepCount)
	return err
}

// ClearScheduledQualityPause only clears the exact legacy quality reason read by
// the runner. The outbox event and state change commit together.
func (r *scheduledTestResultRepository) ClearScheduledQualityPause(ctx context.Context, accountID int64, reason string) (bool, error) {
	if !strings.HasPrefix(reason, "scheduled_quality_check:") {
		return false, nil
	}
	result, err := r.db.ExecContext(ctx, `WITH updated AS (
		UPDATE accounts
		SET temp_unschedulable_until = NULL,
			temp_unschedulable_reason = NULL,
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL AND temp_unschedulable_reason = $2
		RETURNING id
	)
	INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
	SELECT $3, id, NULL, NULL FROM updated
	`, accountID, reason, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// --- scan helpers ---

type scannable interface {
	Scan(dest ...any) error
}

// unmarshalJSONObjectOrNull decodes a JSONB column into dst, treating a NULL
// column and a literal JSON null as "leave dst alone".
func unmarshalJSONObjectOrNull(raw []byte, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return json.Unmarshal(trimmed, dst)
}

func scanPlan(row scannable) (*service.ScheduledTestPlan, error) {
	p := &service.ScheduledTestPlan{}
	var config, audit []byte
	if err := row.Scan(
		&p.ID, &p.AccountID, &p.ModelID, &p.PromptText, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover, &p.QualityCheckEnabled, &p.QualityProvider, &config, &audit,
		&p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	// `{}` unmarshals into both structs; a literal JSON null (or an empty
	// column) would fail and turn the whole plan list into a 500, so treat
	// "no object" as "no config".
	if err := unmarshalJSONObjectOrNull(config, &p.QualityConfig); err != nil {
		return nil, err
	}
	var state service.ChanshuiAuditState
	if err := unmarshalJSONObjectOrNull(audit, &state); err != nil {
		return nil, err
	}
	if state.ID != "" || state.Status == "submitting" {
		p.ActiveAudit = &service.ChanshuiAuditView{ID: state.ID, Status: state.Status, StartedAt: state.StartedAt, ExpiresAt: state.ExpiresAt}
	}
	return p, nil
}

func scanPlans(rows *sql.Rows) ([]*service.ScheduledTestPlan, error) {
	var plans []*service.ScheduledTestPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

func scanPlansWithAccountName(rows *sql.Rows) ([]*service.ScheduledTestPlan, error) {
	var plans []*service.ScheduledTestPlan
	for rows.Next() {
		p := &service.ScheduledTestPlan{}
		if err := rows.Scan(
			&p.ID, &p.AccountID, &p.ModelID, &p.PromptText, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover, &p.QualityCheckEnabled,
			&p.QualityProvider, &p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt, &p.AccountName,
		); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}
