package handler

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	competitiveConcurrencyFanout       = 3
	competitiveConcurrencyFirstOutput  = 3 * time.Second
	competitiveConcurrencyCleanupGrace = 750 * time.Millisecond
	competitiveConcurrencyBufferLimit  = 2 << 20
)

var errCompetitiveResponseBufferLimit = errors.New("competitive response exceeded first-output buffer limit")

type competitiveOpenAISelectFunc func(context.Context, map[int64]struct{}) (*service.AccountSelectionResult, error)

type competitiveOpenAIForwardFunc func(context.Context, *gin.Context, *service.Account) (*service.OpenAIForwardResult, error)

type competitiveOpenAICandidate struct {
	account *service.Account
	release func()
}

type competitiveOpenAIOutcome struct {
	id       int
	account  *service.Account
	ctx      *gin.Context
	result   *service.OpenAIForwardResult
	err      error
	duration time.Duration
}

type competitiveResponseCoordinator struct {
	mu              sync.Mutex
	real            gin.ResponseWriter
	winnerID        int
	winnerAt        time.Time
	winnerCh        chan int
	cancelers       []context.CancelFunc
	selectionCancel context.CancelFunc
	aborted         bool
}

func newCompetitiveResponseCoordinator(real gin.ResponseWriter) *competitiveResponseCoordinator {
	return &competitiveResponseCoordinator{
		real:     real,
		winnerID: -1,
		winnerCh: make(chan int, 1),
	}
}

func (c *competitiveResponseCoordinator) setCancelers(cancelers []context.CancelFunc) {
	c.mu.Lock()
	c.cancelers = append([]context.CancelFunc(nil), cancelers...)
	winnerID := c.winnerID
	aborted := c.aborted
	c.mu.Unlock()
	if winnerID >= 0 {
		for attemptID, cancel := range cancelers {
			if attemptID != winnerID && cancel != nil {
				cancel()
			}
		}
	} else if aborted {
		for _, cancel := range cancelers {
			if cancel != nil {
				cancel()
			}
		}
	}
}

func (c *competitiveResponseCoordinator) setSelectionCancel(cancel context.CancelFunc) {
	c.mu.Lock()
	c.selectionCancel = cancel
	decided := c.winnerID >= 0 || c.aborted
	c.mu.Unlock()
	if decided && cancel != nil {
		cancel()
	}
}

func (c *competitiveResponseCoordinator) isWinner(id int) (winner bool, decided bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.winnerID == id, c.winnerID >= 0 || c.aborted
}

func (c *competitiveResponseCoordinator) hasDecision() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.winnerID >= 0 || c.aborted
}

func (c *competitiveResponseCoordinator) winnerDecisionAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.winnerAt
}

func (c *competitiveResponseCoordinator) tryWin(id int, writer *competitiveResponseWriter) error {
	c.mu.Lock()
	if c.winnerID >= 0 || c.aborted {
		c.mu.Unlock()
		return nil
	}
	c.winnerID = id
	c.winnerAt = time.Now()
	copyResponseHeaders(c.real.Header(), writer.header)
	status := writer.status
	if status <= 0 {
		status = http.StatusOK
	}
	c.real.WriteHeader(status)
	var writeErr error
	if writer.buf.Len() > 0 {
		_, writeErr = c.real.Write(writer.buf.Bytes())
	}
	cancelers := append([]context.CancelFunc(nil), c.cancelers...)
	selectionCancel := c.selectionCancel
	c.mu.Unlock()

	if selectionCancel != nil {
		selectionCancel()
	}
	select {
	case c.winnerCh <- id:
	default:
	}
	for attemptID, cancel := range cancelers {
		if attemptID != id && cancel != nil {
			cancel()
		}
	}
	c.real.Flush()
	return writeErr
}

func (c *competitiveResponseCoordinator) writeWinner(id int, payload []byte) (int, error) {
	winner, decided := c.isWinner(id)
	if decided && !winner {
		return len(payload), nil
	}
	if !winner {
		return len(payload), nil
	}
	return c.real.Write(payload)
}

func (c *competitiveResponseCoordinator) flushWinner(id int) {
	if winner, _ := c.isWinner(id); winner {
		c.real.Flush()
	}
}

func (c *competitiveResponseCoordinator) cancelAll() {
	c.mu.Lock()
	if c.winnerID < 0 {
		c.aborted = true
	}
	cancelers := append([]context.CancelFunc(nil), c.cancelers...)
	selectionCancel := c.selectionCancel
	c.mu.Unlock()
	if selectionCancel != nil {
		selectionCancel()
	}
	for _, cancel := range cancelers {
		if cancel != nil {
			cancel()
		}
	}
}

type competitiveResponseWriter struct {
	mu          sync.Mutex
	coordinator *competitiveResponseCoordinator
	id          int
	header      http.Header
	status      int
	size        int
	written     bool
	buf         bytes.Buffer
}

func newCompetitiveResponseWriter(id int, coordinator *competitiveResponseCoordinator, initial http.Header) *competitiveResponseWriter {
	header := make(http.Header, len(initial))
	copyResponseHeaders(header, initial)
	return &competitiveResponseWriter{
		coordinator: coordinator,
		id:          id,
		header:      header,
		status:      http.StatusOK,
		size:        -1,
	}
}

func (w *competitiveResponseWriter) Header() http.Header { return w.header }

func (w *competitiveResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.written {
		return
	}
	w.status = code
	w.written = true
}

func (w *competitiveResponseWriter) WriteHeaderNow() { w.WriteHeader(w.status) }

func (w *competitiveResponseWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if winner, decided := w.coordinator.isWinner(w.id); decided {
		if !winner {
			return len(payload), nil
		}
		return w.coordinator.writeWinner(w.id, payload)
	}
	if !w.written {
		w.written = true
		w.status = http.StatusOK
	}
	if w.buf.Len()+len(payload) > competitiveConcurrencyBufferLimit {
		return 0, errCompetitiveResponseBufferLimit
	}
	_, _ = w.buf.Write(payload)
	if w.size < 0 {
		w.size = 0
	}
	w.size += len(payload)
	if w.status >= http.StatusBadRequest || !competitivePayloadIsMeaningful(w.header, w.buf.Bytes()) {
		return len(payload), nil
	}
	if err := w.coordinator.tryWin(w.id, w); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (w *competitiveResponseWriter) WriteString(payload string) (int, error) {
	return w.Write([]byte(payload))
}

func (w *competitiveResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *competitiveResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

func (w *competitiveResponseWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
}

func (w *competitiveResponseWriter) Flush() { w.coordinator.flushWinner(w.id) }

func (w *competitiveResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack is unavailable during competitive concurrency")
}

func (w *competitiveResponseWriter) CloseNotify() <-chan bool {
	return w.coordinator.real.CloseNotify()
}

func (w *competitiveResponseWriter) Pusher() http.Pusher { return w.coordinator.real.Pusher() }

func competitivePayloadIsMeaningful(header http.Header, payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return false
	}
	contentType := strings.ToLower(header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") || bytes.Contains(trimmed, []byte("data:")) {
		for _, rawLine := range bytes.Split(trimmed, []byte("\n")) {
			line := bytes.TrimSpace(rawLine)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
				continue
			}
			if !gjson.ValidBytes(data) {
				continue
			}
			if competitiveSSEDataIsMeaningful(data) {
				return true
			}
		}
		return false
	}
	return true
}

func competitiveSSEDataIsMeaningful(data []byte) bool {
	eventType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(data, "type").String()))
	if eventType == "error" || eventType == "response.failed" || gjson.GetBytes(data, "error").Exists() {
		return false
	}

	if strings.HasPrefix(eventType, "response.") && strings.HasSuffix(eventType, ".delta") {
		return strings.TrimSpace(gjson.GetBytes(data, "delta").String()) != ""
	}
	if eventType == "response.output_item.added" {
		itemType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(data, "item.type").String()))
		return (itemType == "function_call" || itemType == "tool_call") &&
			strings.TrimSpace(gjson.GetBytes(data, "item.name").String()) != ""
	}
	if eventType == "content_block_delta" {
		for _, path := range []string{"delta.text", "delta.thinking", "delta.partial_json"} {
			if strings.TrimSpace(gjson.GetBytes(data, path).String()) != "" {
				return true
			}
		}
		return false
	}
	if eventType == "content_block_start" {
		return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(data, "content_block.type").String()), "tool_use") &&
			strings.TrimSpace(gjson.GetBytes(data, "content_block.name").String()) != ""
	}

	choices := gjson.GetBytes(data, "choices")
	if choices.IsArray() {
		meaningful := false
		choices.ForEach(func(_, choice gjson.Result) bool {
			for _, path := range []string{"delta.content", "delta.reasoning_content", "delta.reasoning", "delta.function_call.name", "delta.function_call.arguments"} {
				if strings.TrimSpace(choice.Get(path).String()) != "" {
					meaningful = true
					return false
				}
			}
			toolCalls := choice.Get("delta.tool_calls")
			if toolCalls.IsArray() && len(toolCalls.Array()) > 0 {
				meaningful = true
				return false
			}
			return true
		})
		return meaningful
	}
	return false
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

// groupCompetitiveConcurrencyEnabled 竞争性并发（hedging）已整体停用：
// 恒返回 false，所有竞争路径永不执行，避免向用户重复计落选候选的 input。
// 保留 DB 字段 competitive_concurrency 仅为兼容既有数据，不再生效。
func groupCompetitiveConcurrencyEnabled(apiKey *service.APIKey) bool {
	return false
}

func cloneExcludedAccountIDsForCompetition(src map[int64]struct{}) map[int64]struct{} {
	cloned := make(map[int64]struct{}, len(src)+competitiveConcurrencyFanout)
	for id := range src {
		cloned[id] = struct{}{}
	}
	return cloned
}

func cloneGinContextForCompetition(c *gin.Context, ctx context.Context, writer gin.ResponseWriter) *gin.Context {
	cloned := c.Copy()
	cloned.Writer = writer
	cloned.Request = c.Request.Clone(ctx)
	return cloned
}

func mergeCompetitiveWinnerContext(dst, src *gin.Context) {
	if dst == nil || src == nil {
		return
	}
	for key, value := range src.Keys {
		dst.Set(key, value)
	}
}

func (h *OpenAIGatewayHandler) runCompetitiveOpenAIForward(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	initialSelection *service.AccountSelectionResult,
	initialRelease func(),
	excluded map[int64]struct{},
	model string,
	performanceClass string,
	selectNext competitiveOpenAISelectFunc,
	forward competitiveOpenAIForwardFunc,
	reqLog *zap.Logger,
) (*service.Account, *service.OpenAIForwardResult, error) {
	if initialSelection == nil || initialSelection.Account == nil {
		if initialRelease != nil {
			initialRelease()
		}
		return nil, nil, service.ErrNoAvailableAccounts
	}

	candidates := make([]competitiveOpenAICandidate, 0, competitiveConcurrencyFanout)
	effectiveExcluded := cloneExcludedAccountIDsForCompetition(excluded)
	effectiveExcluded[initialSelection.Account.ID] = struct{}{}

	coordinator := newCompetitiveResponseCoordinator(c.Writer)
	initialHeaders := c.Writer.Header().Clone()
	selectionCtx, cancelSelection := context.WithCancel(c.Request.Context())
	defer cancelSelection()
	coordinator.setSelectionCancel(cancelSelection)
	cancelers := make([]context.CancelFunc, 0, competitiveConcurrencyFanout)
	resultCh := make(chan competitiveOpenAIOutcome, competitiveConcurrencyFanout)
	startedAt := time.Now()
	firstOutputDeadline := startedAt.Add(competitiveConcurrencyFirstOutput)
	startCandidate := func(candidate competitiveOpenAICandidate) {
		attemptID := len(candidates)
		candidates = append(candidates, candidate)
		attemptCtx, cancel := context.WithCancel(c.Request.Context())
		attemptCtx = service.WithCompetitiveUpstreamCancellation(attemptCtx)
		cancelers = append(cancelers, cancel)
		coordinator.setCancelers(cancelers)
		writer := newCompetitiveResponseWriter(attemptID, coordinator, initialHeaders)
		attemptGin := cloneGinContextForCompetition(c, attemptCtx, writer)
		go func(id int, candidate competitiveOpenAICandidate, attemptGin *gin.Context, attemptCtx context.Context) {
			if candidate.release != nil {
				defer candidate.release()
			}
			attemptStart := time.Now()
			result, err := forward(attemptCtx, attemptGin, candidate.account)
			resultCh <- competitiveOpenAIOutcome{
				id:       id,
				account:  candidate.account,
				ctx:      attemptGin,
				result:   result,
				err:      err,
				duration: time.Since(attemptStart),
			}
		}(attemptID, candidate, attemptGin, attemptCtx)
	}
	startCandidate(competitiveOpenAICandidate{account: initialSelection.Account, release: initialRelease})

	hedgeStarted := false
	startHedges := func(reason string) {
		if hedgeStarted || coordinator.hasDecision() {
			return
		}
		hedgeStarted = true
		if reqLog != nil {
			reqLog.Info("openai.competitive_concurrency.hedge_started",
				zap.String("reason", reason),
				zap.Duration("threshold", competitiveConcurrencyFirstOutput),
				zap.Duration("elapsed", time.Since(startedAt)),
			)
		}
		for len(candidates) < competitiveConcurrencyFanout && selectNext != nil {
			selection, err := selectNext(selectionCtx, effectiveExcluded)
			if err != nil || selection == nil || selection.Account == nil {
				break
			}
			if coordinator.hasDecision() {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				break
			}
			if _, duplicate := effectiveExcluded[selection.Account.ID]; duplicate {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				break
			}
			effectiveExcluded[selection.Account.ID] = struct{}{}
			if !selection.Acquired {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				break
			}
			startCandidate(competitiveOpenAICandidate{
				account: selection.Account,
				release: wrapReleaseOnDone(c.Request.Context(), selection.ReleaseFunc),
			})
		}
	}

	remaining := time.Until(firstOutputDeadline)
	if remaining < 0 {
		remaining = 0
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	timerC := timer.C
	outcomes := make(map[int]competitiveOpenAIOutcome, competitiveConcurrencyFanout)
	winnerID := -1
	for winnerID < 0 {
		if hedgeStarted && len(outcomes) == len(candidates) {
			break
		}
		select {
		case winnerID = <-coordinator.winnerCh:
		case outcome := <-resultCh:
			outcomes[outcome.id] = outcome
			if !coordinator.hasDecision() && !hedgeStarted {
				startHedges("initial_attempt_finished")
			}
		case <-timerC:
			timerC = nil
			startHedges("first_output_threshold")
		case <-c.Request.Context().Done():
			coordinator.cancelAll()
			return initialSelection.Account, nil, c.Request.Context().Err()
		}
	}
	if winnerID < 0 {
		select {
		case winnerID = <-coordinator.winnerCh:
		default:
		}
	}
	if winnerID < 0 {
		coordinator.cancelAll()
		markCompetitiveCandidatesExcluded(excluded, candidates)
		account, result, err := competitiveFailureOutcome(candidates, outcomes)
		var failoverErr *service.UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			rememberPendingCompetitiveOpenAIUsage(c, outcomes)
		}
		logCompetitiveSettlementEvidence(reqLog, -1, startedAt, candidates, outcomes, nil)
		return account, result, err
	}

	var winner competitiveOpenAIOutcome
	winnerDone := false
	for !winnerDone {
		if outcome, ok := outcomes[winnerID]; ok {
			winner = outcome
			winnerDone = true
			break
		}
		select {
		case outcome := <-resultCh:
			outcomes[outcome.id] = outcome
		case <-c.Request.Context().Done():
			coordinator.cancelAll()
			return candidates[winnerID].account, nil, c.Request.Context().Err()
		}
	}

	cleanup := time.NewTimer(competitiveConcurrencyCleanupGrace)
	for len(outcomes) < len(candidates) {
		select {
		case outcome := <-resultCh:
			outcomes[outcome.id] = outcome
		case <-cleanup.C:
			goto cleanupComplete
		}
	}
	if !cleanup.Stop() {
		select {
		case <-cleanup.C:
		default:
		}
	}

cleanupComplete:
	mergeCompetitiveWinnerContext(c, winner.ctx)
	if winner.account != nil {
		_ = h.gatewayService.BindStickySession(c.Request.Context(), groupID, sessionHash, winner.account.ID)
	}
	if winner.result != nil {
		aggregateCompetitiveOpenAIUsage(winner.result, winnerID, candidates, outcomes)
		mergePendingCompetitiveOpenAIUsage(c, winner.result)
	}
	h.gatewayService.ReportOpenAICompetitiveRaceOutcome(winner.account.ID, model, performanceClass, true, nil, false)
	logCompetitiveSettlementEvidence(reqLog, winnerID, startedAt, candidates, outcomes, winner.result)
	return winner.account, winner.result, winner.err
}

func markCompetitiveCandidatesExcluded(excluded map[int64]struct{}, candidates []competitiveOpenAICandidate) {
	if excluded == nil {
		return
	}
	for _, candidate := range candidates {
		if candidate.account != nil {
			excluded[candidate.account.ID] = struct{}{}
		}
	}
}

func competitiveFailureOutcome(candidates []competitiveOpenAICandidate, outcomes map[int]competitiveOpenAIOutcome) (*service.Account, *service.OpenAIForwardResult, error) {
	for id := range candidates {
		outcome, ok := outcomes[id]
		if !ok || outcome.err == nil {
			continue
		}
		var failoverErr *service.UpstreamFailoverError
		if errors.As(outcome.err, &failoverErr) {
			return outcome.account, outcome.result, outcome.err
		}
	}
	for id := range candidates {
		if outcome, ok := outcomes[id]; ok {
			if outcome.err != nil {
				return outcome.account, outcome.result, outcome.err
			}
			return outcome.account, outcome.result, errors.New("competitive upstream returned no valid output")
		}
	}
	return candidates[0].account, nil, errors.New("competitive upstream returned no result")
}

func aggregateCompetitiveOpenAIUsage(winner *service.OpenAIForwardResult, winnerID int, candidates []competitiveOpenAICandidate, outcomes map[int]competitiveOpenAIOutcome) {
	if winner == nil || len(candidates) <= 1 {
		return
	}
	baseInput := service.OpenAIUsage{
		InputTokens:              winner.Usage.InputTokens,
		ImageInputTokens:         winner.Usage.ImageInputTokens,
		CacheCreationInputTokens: winner.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     winner.Usage.CacheReadInputTokens,
	}
	for id := range candidates {
		if id == winnerID {
			continue
		}
		usage := baseInput
		if outcome, ok := outcomes[id]; ok && outcome.result != nil && openAIUsageHasTokens(outcome.result.Usage) {
			usage = outcome.result.Usage
		}
		winner.Usage.InputTokens += usage.InputTokens
		winner.Usage.ImageInputTokens += usage.ImageInputTokens
		winner.Usage.OutputTokens += usage.OutputTokens
		winner.Usage.CacheCreationInputTokens += usage.CacheCreationInputTokens
		winner.Usage.CacheReadInputTokens += usage.CacheReadInputTokens
		winner.Usage.ImageOutputTokens += usage.ImageOutputTokens
	}
}

func openAIUsageHasTokens(usage service.OpenAIUsage) bool {
	return usage.InputTokens > 0 || usage.ImageInputTokens > 0 || usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 || usage.ImageOutputTokens > 0
}

const pendingCompetitiveOpenAIUsageKey = "competitive_openai_pending_usage"

func addOpenAIUsage(total *service.OpenAIUsage, usage service.OpenAIUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.ImageInputTokens += usage.ImageInputTokens
	total.OutputTokens += usage.OutputTokens
	total.CacheCreationInputTokens += usage.CacheCreationInputTokens
	total.CacheReadInputTokens += usage.CacheReadInputTokens
	total.ImageOutputTokens += usage.ImageOutputTokens
}

func rememberPendingCompetitiveOpenAIUsage(c *gin.Context, outcomes map[int]competitiveOpenAIOutcome) {
	if c == nil {
		return
	}
	pending, _ := c.Get(pendingCompetitiveOpenAIUsageKey)
	total, _ := pending.(service.OpenAIUsage)
	for _, outcome := range outcomes {
		if outcome.result != nil && openAIUsageHasTokens(outcome.result.Usage) {
			addOpenAIUsage(&total, outcome.result.Usage)
		}
	}
	if openAIUsageHasTokens(total) {
		c.Set(pendingCompetitiveOpenAIUsageKey, total)
	}
}

func mergePendingCompetitiveOpenAIUsage(c *gin.Context, result *service.OpenAIForwardResult) {
	if c == nil || result == nil {
		return
	}
	pending, ok := c.Get(pendingCompetitiveOpenAIUsageKey)
	if !ok {
		return
	}
	c.Set(pendingCompetitiveOpenAIUsageKey, nil)
	usage, ok := pending.(service.OpenAIUsage)
	if ok {
		addOpenAIUsage(&result.Usage, usage)
	}
}

func logCompetitiveSettlementEvidence(reqLog *zap.Logger, winnerID int, startedAt time.Time, candidates []competitiveOpenAICandidate, outcomes map[int]competitiveOpenAIOutcome, winner *service.OpenAIForwardResult) {
	if reqLog == nil {
		return
	}
	evidence := make([]map[string]any, 0, len(candidates))
	for id, candidate := range candidates {
		item := map[string]any{
			"attempt":    id + 1,
			"account_id": candidate.account.ID,
			"winner":     id == winnerID,
		}
		if outcome, ok := outcomes[id]; ok {
			item["duration_ms"] = outcome.duration.Milliseconds()
			item["reported_usage"] = outcome.result != nil && openAIUsageHasTokens(outcome.result.Usage)
			if outcome.err != nil {
				item["error"] = outcome.err.Error()
			}
		} else {
			item["settlement"] = "estimated_from_winner_input"
		}
		evidence = append(evidence, item)
	}
	reqLog.Info("openai.competitive_concurrency.settlement_evidence",
		zap.Int("attempt_count", len(candidates)),
		zap.Int64("elapsed_ms", time.Since(startedAt).Milliseconds()),
		zap.Bool("usage_aggregated_into_winner", winner != nil),
		zap.Any("attempts", evidence),
	)
}
