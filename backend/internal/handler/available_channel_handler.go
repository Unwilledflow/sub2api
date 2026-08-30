package handler

import (
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AvailableChannelHandler 处理用户侧「可用渠道」查询。
//
// 用户侧接口委托 ChannelService.ListAvailable，并在返回前做四层过滤：
//  1. 行过滤：只保留状态为 Active 且与当前用户可访问分组有交集的渠道；
//  2. 分组过滤：渠道的 Groups 只保留用户可访问的那些；
//  3. 平台过滤：普通分组只保留自身平台模型；Composite 分组按渠道已配置的具体模型平台
//     展开。这样既防止普通分组跨平台泄漏，也让 Composite 正确展示其多平台能力；
//  4. 字段白名单：仅返回用户需要的字段（省略 BillingModelSource / RestrictModels
//     / 内部 ID / Status 等管理字段）。
type AvailableChannelHandler struct {
	channelService *service.ChannelService
	apiKeyService  *service.APIKeyService
	settingService *service.SettingService
	accountRepo    service.AccountRepository
	pricingService *service.PricingService
}

// NewAvailableChannelHandler creates the user-facing available-channel handler.
func NewAvailableChannelHandler(
	channelService *service.ChannelService,
	apiKeyService *service.APIKeyService,
	settingService *service.SettingService,
	accountRepo service.AccountRepository,
	pricingService *service.PricingService,
) *AvailableChannelHandler {
	return &AvailableChannelHandler{
		channelService: channelService,
		apiKeyService:  apiKeyService,
		settingService: settingService,
		accountRepo:    accountRepo,
		pricingService: pricingService,
	}
}

func (h *AvailableChannelHandler) featureEnabled(c *gin.Context) bool {
	if h.settingService == nil {
		return false
	}
	return h.settingService.GetAvailableChannelsRuntime(c.Request.Context()).Enabled
}

type userAvailableGroup struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Platform         string  `json:"platform"`
	SubscriptionType string  `json:"subscription_type"`
	// RateMultiplier / peak fields power the model-marketplace "group-adjusted
	// price" math. They are intentionally NOT rendered as raw badges on the
	// available-channels page (that exposed internal rates to users).
	RateMultiplier     float64 `json:"rate_multiplier"`
	PeakRateEnabled    bool    `json:"peak_rate_enabled"`
	PeakStart          string  `json:"peak_start"`
	PeakEnd            string  `json:"peak_end"`
	PeakRateMultiplier float64 `json:"peak_rate_multiplier"`
	IsExclusive        bool    `json:"is_exclusive"`
}

type userSupportedModelPricing struct {
	BillingMode      string                   `json:"billing_mode"`
	InputPrice       *float64                 `json:"input_price"`
	OutputPrice      *float64                 `json:"output_price"`
	CacheWritePrice  *float64                 `json:"cache_write_price"`
	CacheReadPrice   *float64                 `json:"cache_read_price"`
	ImageInputPrice  *float64                 `json:"image_input_price"`
	ImageOutputPrice *float64                 `json:"image_output_price"`
	PerRequestPrice  *float64                 `json:"per_request_price"`
	Intervals        []userPricingIntervalDTO `json:"intervals"`
}

type userPricingIntervalDTO struct {
	MinTokens       int      `json:"min_tokens"`
	MaxTokens       *int     `json:"max_tokens"`
	TierLabel       string   `json:"tier_label,omitempty"`
	InputPrice      *float64 `json:"input_price"`
	OutputPrice     *float64 `json:"output_price"`
	CacheWritePrice *float64 `json:"cache_write_price"`
	CacheReadPrice  *float64 `json:"cache_read_price"`
	PerRequestPrice *float64 `json:"per_request_price"`
}

type userSupportedModel struct {
	Name     string                     `json:"name"`
	Platform string                     `json:"platform"`
	Pricing  *userSupportedModelPricing `json:"pricing"`
}

type userChannelPlatformSection struct {
	Platform        string               `json:"platform"`
	Groups          []userAvailableGroup `json:"groups"`
	SupportedModels []userSupportedModel `json:"supported_models"`
}

type userAvailableChannel struct {
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Platforms   []userChannelPlatformSection `json:"platforms"`
}

// List returns channels and models visible to the current user.
// GET /api/v1/channels/available
func (h *AvailableChannelHandler) List(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	if !h.featureEnabled(c) {
		response.Success(c, []userAvailableChannel{})
		return
	}

	role, _ := middleware.GetUserRoleFromContext(c)
	isAdmin := role == service.RoleAdmin

	var allowedGroupIDs map[int64]struct{}
	if !isAdmin {
		userGroups, err := h.apiKeyService.GetAvailableGroups(c.Request.Context(), subject.UserID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		allowedGroupIDs = make(map[int64]struct{}, len(userGroups))
		for i := range userGroups {
			allowedGroupIDs[userGroups[i].ID] = struct{}{}
		}
	}

	channels, err := h.channelService.ListAvailable(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]userAvailableChannel, 0, len(channels))
	for _, ch := range channels {
		if ch.Status != service.StatusActive {
			continue
		}
		visibleGroups := filterUserVisibleGroups(ch.Groups, allowedGroupIDs)
		if len(visibleGroups) == 0 {
			continue
		}
		sections := buildPlatformSections(ch, visibleGroups)
		if len(sections) == 0 {
			continue
		}
		out = append(out, userAvailableChannel{
			Name:        ch.Name,
			Description: ch.Description,
			Platforms:   sections,
		})
	}

	accountChannels, err := h.listAvailableFromAccounts(c, allowedGroupIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out = mergeUserAvailableChannels(out, accountChannels)

	response.Success(c, out)
}

func mergeUserAvailableChannels(
	configured []userAvailableChannel,
	accountBacked []userAvailableChannel,
) []userAvailableChannel {
	merged := make([]userAvailableChannel, 0, len(configured)+len(accountBacked))
	merged = append(merged, configured...)
	merged = append(merged, accountBacked...)
	sort.SliceStable(merged, func(i, j int) bool {
		return strings.ToLower(merged[i].Name) < strings.ToLower(merged[j].Name)
	})
	return merged
}

func (h *AvailableChannelHandler) listAvailableFromAccounts(
	c *gin.Context,
	allowedGroupIDs map[int64]struct{},
) ([]userAvailableChannel, error) {
	if h.accountRepo == nil {
		return []userAvailableChannel{}, nil
	}

	accounts, err := h.accountRepo.ListSchedulable(c.Request.Context())
	if err != nil {
		return nil, err
	}

	// Merge accounts that expose the same platform/type and the same visible
	// group set into a single channel row, so users never see one group
	// repeated once per backing account.
	type mergeKey struct {
		name     string
		platform string
		groupIDs string
	}
	merged := make(map[mergeKey]*userAvailableChannel)
	order := make([]mergeKey, 0)

	for i := range accounts {
		acc := accounts[i]
		visibleGroups := h.accountVisibleGroups(&acc, allowedGroupIDs)
		if len(visibleGroups) == 0 {
			continue
		}

		models := h.accountSupportedModels(&acc)
		if len(models) == 0 {
			continue
		}

		name := accountChannelDescription(&acc)
		ids := make([]string, 0, len(visibleGroups))
		for _, g := range visibleGroups {
			ids = append(ids, strconv.FormatInt(g.ID, 10))
		}
		sort.Strings(ids)
		key := mergeKey{name: name, platform: acc.Platform, groupIDs: strings.Join(ids, ",")}

		if existing, ok := merged[key]; ok {
			existing.Platforms[0].SupportedModels = mergeSupportedModels(
				existing.Platforms[0].SupportedModels, models)
			continue
		}
		merged[key] = &userAvailableChannel{
			// Do NOT expose the internal account name (acc.Name) to users —
			// it may contain operator-side identifiers such as "proapi - Gemini 反重力".
			// Use the platform/type description as the display name instead.
			Name:        name,
			Description: "",
			Platforms: []userChannelPlatformSection{
				{
					Platform:        acc.Platform,
					Groups:          visibleGroups,
					SupportedModels: models,
				},
			},
		}
		order = append(order, key)
	}

	out := make([]userAvailableChannel, 0, len(order))
	for _, key := range order {
		out = append(out, *merged[key])
	}

	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// mergeSupportedModels unions two model lists by name, keeping the first
// occurrence's pricing.
func mergeSupportedModels(base, extra []userSupportedModel) []userSupportedModel {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]userSupportedModel, 0, len(base)+len(extra))
	for _, m := range append(append([]userSupportedModel{}, base...), extra...) {
		if _, ok := seen[m.Name]; ok {
			continue
		}
		seen[m.Name] = struct{}{}
		out = append(out, m)
	}
	return out
}

func (h *AvailableChannelHandler) accountVisibleGroups(
	acc *service.Account,
	allowedGroupIDs map[int64]struct{},
) []userAvailableGroup {
	if acc == nil || len(acc.Groups) == 0 {
		return nil
	}

	visible := make([]userAvailableGroup, 0, len(acc.Groups))
	for _, g := range acc.Groups {
		if g == nil {
			continue
		}
		if allowedGroupIDs != nil {
			if _, ok := allowedGroupIDs[g.ID]; !ok {
				continue
			}
		}
		visible = append(visible, userAvailableGroup{
			ID:                 g.ID,
			Name:               g.Name,
			Platform:           g.Platform,
			SubscriptionType:   g.SubscriptionType,
			RateMultiplier:     g.RateMultiplier,
			PeakRateEnabled:    g.PeakRateEnabled,
			PeakStart:          g.PeakStart,
			PeakEnd:            g.PeakEnd,
			PeakRateMultiplier: g.PeakRateMultiplier,
			IsExclusive:        g.IsExclusive,
		})
	}

	sort.SliceStable(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })
	return visible
}

func (h *AvailableChannelHandler) accountSupportedModels(acc *service.Account) []userSupportedModel {
	if acc == nil {
		return nil
	}

	mapping := acc.GetModelMapping()
	models := make([]userSupportedModel, 0, len(mapping))
	for displayModel, upstreamModel := range mapping {
		displayModel = strings.TrimSpace(displayModel)
		if displayModel == "" || strings.Contains(displayModel, "*") {
			continue
		}
		models = append(models, userSupportedModel{
			Name:     displayModel,
			Platform: acc.Platform,
			Pricing:  h.pricingForMappedModel(displayModel, upstreamModel),
		})
	}

	sort.SliceStable(models, func(i, j int) bool {
		return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name)
	})
	return models
}

func (h *AvailableChannelHandler) pricingForMappedModel(displayModel, upstreamModel string) *userSupportedModelPricing {
	if h.pricingService == nil {
		return nil
	}

	candidates := []string{strings.TrimSpace(upstreamModel), strings.TrimSpace(displayModel)}
	for _, model := range candidates {
		if model == "" || strings.Contains(model, "*") {
			continue
		}
		if pricing := h.pricingService.GetModelPricing(model); pricing != nil {
			return toUserPricing(displayPricingFromLiteLLM(displayModel, pricing))
		}
	}
	return nil
}

func displayPricingFromLiteLLM(model string, lp *service.LiteLLMModelPricing) *service.ChannelModelPricing {
	if lp == nil {
		return nil
	}
	mode := service.BillingModeToken
	if lp.Mode == "image_generation" {
		mode = service.BillingModeImage
	}
	return &service.ChannelModelPricing{
		Models:           []string{model},
		BillingMode:      mode,
		InputPrice:       nonZeroFloatPtr(lp.InputCostPerToken),
		OutputPrice:      nonZeroFloatPtr(lp.OutputCostPerToken),
		CacheWritePrice:  nonZeroFloatPtr(lp.CacheCreationInputTokenCost),
		CacheReadPrice:   nonZeroFloatPtr(lp.CacheReadInputTokenCost),
		ImageOutputPrice: nonZeroFloatPtr(lp.OutputCostPerImageToken),
		PerRequestPrice:  nonZeroFloatPtr(lp.OutputCostPerImage),
	}
}

func nonZeroFloatPtr(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}

func accountChannelDescription(acc *service.Account) string {
	if acc == nil {
		return ""
	}
	parts := []string{strings.TrimSpace(acc.Platform)}
	if typ := strings.TrimSpace(acc.Type); typ != "" {
		parts = append(parts, typ)
	}
	return strings.Join(parts, " / ")
}

// buildPlatformSections 把一个渠道按 visibleGroups 的平台集合拆成有序的 section 列表：
// 每个 section 对应一个具体平台，只包含该平台的 groups 和 supported_models。
//
// Composite 分组可访问渠道中所有已配置的具体平台，因此会被展开到每个有支持模型的
// 平台 section。普通分组仍严格留在自身平台，避免跨平台模型信息泄漏。Composite 渠道
// 尚未配置任何模型时保留 composite section，以便前端继续展示该分组和“未配置模型”状态。
// 输出按 platform 字母序稳定排序，便于前端等效比较与回归测试。
func buildPlatformSections(
	ch service.AvailableChannel,
	visibleGroups []userAvailableGroup,
) []userChannelPlatformSection {
	groupsByPlatform := make(map[string][]userAvailableGroup, 4)
	compositeGroups := make([]userAvailableGroup, 0, 1)
	for _, g := range visibleGroups {
		if g.Platform == "" {
			continue
		}
		if g.Platform == service.PlatformComposite {
			compositeGroups = append(compositeGroups, g)
			continue
		}
		groupsByPlatform[g.Platform] = append(groupsByPlatform[g.Platform], g)
	}

	if len(compositeGroups) > 0 {
		modelPlatforms := make(map[string]struct{}, len(ch.SupportedModels))
		for i := range ch.SupportedModels {
			if platform := ch.SupportedModels[i].Platform; platform != "" {
				modelPlatforms[platform] = struct{}{}
			}
		}
		if len(modelPlatforms) == 0 {
			groupsByPlatform[service.PlatformComposite] = append(
				groupsByPlatform[service.PlatformComposite],
				compositeGroups...,
			)
		} else {
			for platform := range modelPlatforms {
				groupsByPlatform[platform] = append(groupsByPlatform[platform], compositeGroups...)
			}
		}
	}
	if len(groupsByPlatform) == 0 {
		return nil
	}

	platforms := make([]string, 0, len(groupsByPlatform))
	for p := range groupsByPlatform {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)

	sections := make([]userChannelPlatformSection, 0, len(platforms))
	for _, platform := range platforms {
		platformSet := map[string]struct{}{platform: {}}
		sections = append(sections, userChannelPlatformSection{
			Platform:        platform,
			Groups:          groupsByPlatform[platform],
			SupportedModels: toUserSupportedModels(ch.SupportedModels, platformSet),
		})
	}
	return sections
}

func filterUserVisibleGroups(
	groups []service.AvailableGroupRef,
	allowed map[int64]struct{},
) []userAvailableGroup {
	visible := make([]userAvailableGroup, 0, len(groups))
	for _, g := range groups {
		if allowed != nil {
			if _, ok := allowed[g.ID]; !ok {
				continue
			}
		}
		visible = append(visible, userAvailableGroup{
			ID:                 g.ID,
			Name:               g.Name,
			Platform:           g.Platform,
			SubscriptionType:   g.SubscriptionType,
			RateMultiplier:     g.RateMultiplier,
			PeakRateEnabled:    g.PeakRateEnabled,
			PeakStart:          g.PeakStart,
			PeakEnd:            g.PeakEnd,
			PeakRateMultiplier: g.PeakRateMultiplier,
			IsExclusive:        g.IsExclusive,
		})
	}
	return visible
}

func toUserSupportedModels(
	src []service.SupportedModel,
	allowedPlatforms map[string]struct{},
) []userSupportedModel {
	out := make([]userSupportedModel, 0, len(src))
	for i := range src {
		m := src[i]
		if allowedPlatforms != nil {
			if _, ok := allowedPlatforms[m.Platform]; !ok {
				continue
			}
		}
		out = append(out, userSupportedModel{
			Name:     m.Name,
			Platform: m.Platform,
			Pricing:  toUserPricing(m.Pricing),
		})
	}
	return out
}

// toUserPricingIntervals 将定价区间转换为用户 DTO 白名单形态；nil 入参返回 nil（JSON omitempty 可省略）。
func toUserPricingIntervals(src []service.PricingInterval) []userPricingIntervalDTO {
	if src == nil {
		return nil
	}
	intervals := make([]userPricingIntervalDTO, 0, len(src))
	for _, iv := range src {
		intervals = append(intervals, userPricingIntervalDTO{
			MinTokens:       iv.MinTokens,
			MaxTokens:       iv.MaxTokens,
			TierLabel:       iv.TierLabel,
			InputPrice:      iv.InputPrice,
			OutputPrice:     iv.OutputPrice,
			CacheWritePrice: iv.CacheWritePrice,
			CacheReadPrice:  iv.CacheReadPrice,
			PerRequestPrice: iv.PerRequestPrice,
		})
	}
	return intervals
}

// toUserPricing 将 service 层定价转换为用户 DTO；入参为 nil 时返回 nil。
func toUserPricing(p *service.ChannelModelPricing) *userSupportedModelPricing {
	if p == nil {
		return nil
	}
	intervals := toUserPricingIntervals(p.Intervals)
	if intervals == nil {
		// 用户侧定价的 intervals 固定输出数组（空配置为 []），保持既有契约。
		intervals = []userPricingIntervalDTO{}
	}
	billingMode := string(p.BillingMode)
	if billingMode == "" {
		billingMode = string(service.BillingModeToken)
	}
	return &userSupportedModelPricing{
		BillingMode:      billingMode,
		InputPrice:       p.InputPrice,
		OutputPrice:      p.OutputPrice,
		CacheWritePrice:  p.CacheWritePrice,
		CacheReadPrice:   p.CacheReadPrice,
		ImageInputPrice:  p.ImageInputPrice,
		ImageOutputPrice: p.ImageOutputPrice,
		PerRequestPrice:  p.PerRequestPrice,
		Intervals:        intervals,
	}
}
