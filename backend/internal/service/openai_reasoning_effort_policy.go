package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	maxReasoningEffortMappings = 64
	maxReasoningEffortValueLen = 64
)

var openAIReasoningEffortValues = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

type openAIReasoningEffortPolicyContextKey struct{}
type requestedReasoningEffortContextKey struct{}

type openAIReasoningEffortPolicy struct {
	maxEffort string
	overLimit string
	mappings  []ReasoningEffortMapping
}

const ReasoningEffortOverLimitDeny = "deny"

type ReasoningEffortOverLimitError struct {
	Requested string
	Max       string
}

func (e *ReasoningEffortOverLimitError) Error() string {
	if e == nil {
		return "reasoning effort exceeds this group's limit"
	}
	requested := strings.TrimSpace(e.Requested)
	max := strings.TrimSpace(e.Max)
	if requested == "" && max == "" {
		return "reasoning effort exceeds this group's limit"
	}
	if requested == "" {
		return fmt.Sprintf("reasoning effort exceeds this group's limit of %q", max)
	}
	if max == "" {
		return fmt.Sprintf("reasoning effort %q exceeds this group's limit", requested)
	}
	return fmt.Sprintf("reasoning effort %q exceeds this group's limit of %q", requested, max)
}

type ReasoningEffortMappingDeniedError struct {
	Requested string
}

func (e *ReasoningEffortMappingDeniedError) Error() string {
	if e == nil {
		return "reasoning effort is denied by this group's mapping policy"
	}
	requested := strings.TrimSpace(e.Requested)
	if requested == "" {
		return "reasoning effort is denied by this group's mapping policy"
	}
	return fmt.Sprintf("reasoning effort %q is denied by this group's mapping policy", requested)
}

func IsReasoningEffortPolicyDenied(err error) bool {
	if err == nil {
		return false
	}
	var overLimit *ReasoningEffortOverLimitError
	if errors.As(err, &overLimit) {
		return true
	}
	var mappingDenied *ReasoningEffortMappingDeniedError
	return errors.As(err, &mappingDenied)
}

func NormalizeMaxReasoningEffortOverLimit(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case ReasoningEffortOverLimitDeny, "reject", "block":
		return ReasoningEffortOverLimitDeny
	default:
		return ""
	}
}

// NormalizeMaxReasoningEffort validates and canonicalizes a group policy value.
// Empty means that the group does not impose a ceiling.
func NormalizeMaxReasoningEffort(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	switch value {
	case "":
		return ""
	case "minimal":
		return "minimal"
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "extrahigh":
		return "xhigh"
	case "max":
		return "max"
	default:
		return ""
	}
}

func reasoningEffortValuesForPlatform(platform string) []string {
	if platform != PlatformOpenAI && platform != PlatformComposite && platform != PlatformAdaptive {
		return nil
	}
	return openAIReasoningEffortValues
}

func normalizeMaxReasoningEffortForPlatform(platform, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	allowedValues := reasoningEffortValuesForPlatform(platform)
	if len(allowedValues) == 0 {
		return "", fmt.Errorf(
			"reasoning effort policy is only supported for platforms %q, %q and %q",
			PlatformOpenAI,
			PlatformComposite,
			PlatformAdaptive,
		)
	}

	value := NormalizeMaxReasoningEffort(raw)
	for _, allowed := range allowedValues {
		if value == allowed {
			return value, nil
		}
	}
	return "", fmt.Errorf(
		"reasoning effort %q is not supported for platform %q; allowed values: %s",
		raw,
		platform,
		strings.Join(allowedValues, ", "),
	)
}

func reasoningEffortRank(raw string) (int, bool) {
	switch NormalizeMaxReasoningEffort(raw) {
	case "minimal":
		return 1, true
	case "low":
		return 2, true
	case "medium":
		return 3, true
	case "high":
		return 4, true
	case "xhigh":
		return 5, true
	case "max":
		return 6, true
	default:
		return 0, false
	}
}

// NormalizeReasoningEffortMappings validates group mapping rules against the
// fixed effort values supported by OpenAI routes.
func NormalizeReasoningEffortMappings(platform string, raw []ReasoningEffortMapping) ([]ReasoningEffortMapping, error) {
	if len(raw) > maxReasoningEffortMappings {
		return nil, fmt.Errorf("reasoning effort mappings cannot exceed %d entries", maxReasoningEffortMappings)
	}

	normalized := make([]ReasoningEffortMapping, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, mapping := range raw {
		from := NormalizeMaxReasoningEffort(mapping.From)
		to := NormalizeMaxReasoningEffort(mapping.To)
		if from == "" || to == "" {
			return nil, fmt.Errorf("reasoning effort mapping %d contains an empty or unknown value", i+1)
		}
		if len(from) > maxReasoningEffortValueLen || len(to) > maxReasoningEffortValueLen {
			return nil, fmt.Errorf("reasoning effort mapping %d values cannot exceed %d characters", i+1, maxReasoningEffortValueLen)
		}
		if _, err := normalizeMaxReasoningEffortForPlatform(platform, from); err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d source: %w", i+1, err)
		}
		if _, err := normalizeMaxReasoningEffortForPlatform(platform, to); err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d target: %w", i+1, err)
		}
		key := from
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate reasoning effort mapping source %q", from)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, ReasoningEffortMapping{From: from, To: to})
	}
	return normalized, nil
}

// WithOpenAIReasoningEffortPolicy binds a group policy to a request after its
// concrete target platform has been resolved to OpenAI. The policy is copied so
// retries and asynchronous forwarding cannot observe later slice mutations.
func WithRequestedReasoningEffort(ctx context.Context, effort string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ctx
	}
	return context.WithValue(ctx, requestedReasoningEffortContextKey{}, effort)
}

func RequestedReasoningEffortFromContext(ctx context.Context) *string {
	if ctx == nil {
		return nil
	}
	value, ok := ctx.Value(requestedReasoningEffortContextKey{}).(string)
	if !ok {
		return nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func CanonicalRequestedReasoningEffort(body []byte, modelCandidates ...string) *string {
	if raw := explicitRequestedReasoningEffortFromBody(body); raw != "" {
		canonical := NormalizeMaxReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	for _, model := range modelCandidates {
		if value := canonicalReasoningEffortFromModelSuffix(model); value != "" {
			return &value
		}
	}
	if model := strings.TrimSpace(gjson.GetBytes(body, "model").String()); model != "" {
		if value := canonicalReasoningEffortFromModelSuffix(model); value != "" {
			return &value
		}
	}
	return nil
}

func explicitRequestedReasoningEffortFromBody(body []byte) string {
	// Each gjson.GetBytes call is a full body rescan, and the misses below are
	// the common case. Skip a probe when its key text cannot appear in the body;
	// escaped keys ("\u0072easoning") must still work, so a body containing any
	// backslash keeps the original unconditional behaviour.
	hasEscape := bytes.IndexByte(body, '\\') >= 0
	for _, probe := range reasoningEffortProbes {
		if !hasEscape && !bytes.Contains(body, probe.key) {
			continue
		}
		if raw := strings.TrimSpace(gjson.GetBytes(body, probe.path).String()); raw != "" {
			return raw
		}
	}
	return ""
}

type reasoningEffortProbe struct {
	path string
	key  []byte
}

// reasoningEffortProbes 按优先级排列，与原先的短路顺序一致。
var reasoningEffortProbes = []reasoningEffortProbe{
	{"reasoning.effort", []byte(`"reasoning"`)},
	{"reasoning_effort", []byte(`"reasoning_effort"`)},
	{"output_config.effort", []byte(`"output_config"`)},
}

func canonicalReasoningEffortFromModelSuffix(model string) string {
	if strings.TrimSpace(model) == "" {		return ""
	}
	modelID := strings.TrimSpace(model)
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}
	parts := strings.FieldsFunc(strings.ToLower(modelID), func(r rune) bool {
		switch r {
		case '-', '_', ' ':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}
	return NormalizeMaxReasoningEffort(parts[len(parts)-1])
}

func WithOpenAIReasoningEffortPolicy(ctx context.Context, maxEffort string, mappings []ReasoningEffortMapping, overLimit ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	limit := ""
	if len(overLimit) > 0 {
		limit = NormalizeMaxReasoningEffortOverLimit(overLimit[0])
	}
	policy := openAIReasoningEffortPolicy{
		maxEffort: maxEffort,
		overLimit: limit,
		mappings:  append([]ReasoningEffortMapping(nil), mappings...),
	}
	return context.WithValue(ctx, openAIReasoningEffortPolicyContextKey{}, policy)
}

// ApplyOpenAIReasoningEffortPolicyFromContext applies a policy previously bound
// to the request. An unbound request is returned byte-for-byte unchanged.
func ApplyOpenAIReasoningEffortPolicyFromContext(ctx context.Context, body []byte) ([]byte, bool, error) {
	if ctx == nil {
		return body, false, nil
	}
	policy, ok := ctx.Value(openAIReasoningEffortPolicyContextKey{}).(openAIReasoningEffortPolicy)
	if !ok {
		return body, false, nil
	}
	return ApplyOpenAIReasoningEffortPolicy(body, policy.maxEffort, policy.mappings, policy.overLimit)
}

func applyOpenAIWSReasoningEffortPolicy(payload []byte, hooks *OpenAIWSIngressHooks) ([]byte, error) {
	if hooks == nil || (hooks.MaxReasoningEffort == "" && len(hooks.ReasoningEffortMappings) == 0) {
		return payload, nil
	}
	capped, changed, policyErr := ApplyOpenAIReasoningEffortPolicy(payload, hooks.MaxReasoningEffort, hooks.ReasoningEffortMappings, hooks.MaxReasoningEffortOverLimit)
	if policyErr != nil {
		return payload, policyErr
	}
	if changed {
		return capped, nil
	}
	return payload, nil
}

func mapReasoningEffort(raw string, mappings []ReasoningEffortMapping) (string, bool) {
	value := strings.TrimSpace(raw)
	canonical := NormalizeMaxReasoningEffort(value)
	for _, mapping := range mappings {
		if canonical != "" && canonical == NormalizeMaxReasoningEffort(mapping.From) {
			return strings.TrimSpace(mapping.To), true
		}
	}
	return value, false
}

func sanitizeGroupReasoningEffortPolicy(group *Group) {
	if group == nil {
		return
	}
	maxEffort, maxErr := normalizeMaxReasoningEffortForPlatform(group.Platform, group.MaxReasoningEffort)
	mappings, mappingsErr := NormalizeReasoningEffortMappings(group.Platform, group.ReasoningEffortMappings)
	if maxErr != nil {
		maxEffort = ""
	}
	if mappingsErr != nil {
		mappings = []ReasoningEffortMapping{}
	}
	group.MaxReasoningEffort = maxEffort
	group.ReasoningEffortMappings = mappings
}

// ApplyOpenAIReasoningEffortPolicy applies one exact mapping and then caps
// known effort levels. Omitted values remain untouched so upstream defaults
// stay in control.
func ApplyOpenAIReasoningEffortPolicy(body []byte, maxEffort string, mappings []ReasoningEffortMapping, overLimit ...string) ([]byte, bool, error) {
	limit := ""
	if len(overLimit) > 0 {
		limit = NormalizeMaxReasoningEffortOverLimit(overLimit[0])
	}
	maxRank, hasMax := reasoningEffortRank(maxEffort)
	if len(body) == 0 || (!hasMax && len(mappings) == 0) {
		return body, false, nil
	}

	result := body
	changed := false
	for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
		field := gjson.GetBytes(result, path)
		if !field.Exists() || field.Type != gjson.String {
			continue
		}
		original := strings.TrimSpace(field.String())
		if original == "" {
			continue
		}

		effective, _ := mapReasoningEffort(original, mappings)
		if currentRank, recognized := reasoningEffortRank(effective); recognized {
			effective = NormalizeMaxReasoningEffort(effective)
			if hasMax && currentRank > maxRank {
				if limit == ReasoningEffortOverLimitDeny {
					return body, false, &ReasoningEffortOverLimitError{Requested: original, Max: NormalizeMaxReasoningEffort(maxEffort)}
				}
				effective = NormalizeMaxReasoningEffort(maxEffort)
			}
		}
		if effective == original {
			continue
		}

		updated, err := sjson.SetBytes(result, path, effective)
		if err != nil {
			continue
		}
		result = updated
		changed = true
	}
	return result, changed, nil
}
