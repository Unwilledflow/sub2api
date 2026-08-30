package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageLogFromService_IncludesOpenAIWSMode(t *testing.T) {
	t.Parallel()

	wsLog := &service.UsageLog{
		RequestID:    "req_1",
		Model:        "gpt-5.3-codex",
		OpenAIWSMode: true,
	}
	httpLog := &service.UsageLog{
		RequestID:    "resp_1",
		Model:        "gpt-5.3-codex",
		OpenAIWSMode: false,
	}

	require.True(t, UsageLogFromService(wsLog).OpenAIWSMode)
	require.False(t, UsageLogFromService(httpLog).OpenAIWSMode)
	require.True(t, UsageLogFromServiceAdmin(wsLog).OpenAIWSMode)
	require.False(t, UsageLogFromServiceAdmin(httpLog).OpenAIWSMode)
}

func TestUsageLogFromService_PrefersRequestTypeForLegacyFields(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:    "req_2",
		Model:        "gpt-5.3-codex",
		RequestType:  service.RequestTypeWSV2,
		Stream:       false,
		OpenAIWSMode: false,
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.Equal(t, "ws_v2", userDTO.RequestType)
	require.True(t, userDTO.Stream)
	require.True(t, userDTO.OpenAIWSMode)
	require.Equal(t, "ws_v2", adminDTO.RequestType)
	require.True(t, adminDTO.Stream)
	require.True(t, adminDTO.OpenAIWSMode)
}

func TestUsageCleanupTaskFromService_RequestTypeMapping(t *testing.T) {
	t.Parallel()

	requestType := int16(service.RequestTypeStream)
	task := &service.UsageCleanupTask{
		ID:     1,
		Status: service.UsageCleanupStatusPending,
		Filters: service.UsageCleanupFilters{
			RequestType: &requestType,
		},
	}

	dtoTask := UsageCleanupTaskFromService(task)
	require.NotNil(t, dtoTask)
	require.NotNil(t, dtoTask.Filters.RequestType)
	require.Equal(t, "stream", *dtoTask.Filters.RequestType)
}

func TestRequestTypeStringPtrNil(t *testing.T) {
	t.Parallel()
	require.Nil(t, requestTypeStringPtr(nil))
}

func TestUsageLogFromService_IncludesServiceTierForUserAndAdmin(t *testing.T) {
	t.Parallel()

	serviceTier := "priority"
	inboundEndpoint := "/v1/chat/completions"
	upstreamEndpoint := "/v1/responses"
	log := &service.UsageLog{
		RequestID:             "req_3",
		Model:                 "gpt-5.4",
		ServiceTier:           &serviceTier,
		InboundEndpoint:       &inboundEndpoint,
		UpstreamEndpoint:      &upstreamEndpoint,
		AccountRateMultiplier: f64Ptr(1.5),
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.NotNil(t, userDTO.ServiceTier)
	require.Equal(t, serviceTier, *userDTO.ServiceTier)
	require.NotNil(t, userDTO.InboundEndpoint)
	require.Equal(t, inboundEndpoint, *userDTO.InboundEndpoint)
	require.Nil(t, userDTO.UpstreamEndpoint)
	require.NotNil(t, adminDTO.ServiceTier)
	require.Equal(t, serviceTier, *adminDTO.ServiceTier)
	require.NotNil(t, adminDTO.InboundEndpoint)
	require.Equal(t, inboundEndpoint, *adminDTO.InboundEndpoint)
	require.NotNil(t, adminDTO.UpstreamEndpoint)
	require.Equal(t, upstreamEndpoint, *adminDTO.UpstreamEndpoint)
	require.NotNil(t, adminDTO.AccountRateMultiplier)
	require.InDelta(t, 1.5, *adminDTO.AccountRateMultiplier, 1e-12)
}

func TestUsageLogFromService_UsesRequestedModelAndKeepsUpstreamAdminOnly(t *testing.T) {
	t.Parallel()

	upstreamModel := "claude-sonnet-4-20250514"
	log := &service.UsageLog{
		RequestID:      "req_4",
		Model:          upstreamModel,
		RequestedModel: "claude-sonnet-4",
		UpstreamModel:  &upstreamModel,
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.Equal(t, "claude-sonnet-4", userDTO.Model)
	require.Equal(t, "claude-sonnet-4", adminDTO.Model)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "upstream_model")

	adminJSON, err := json.Marshal(adminDTO)
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"upstream_model":"claude-sonnet-4-20250514"`)
}

func TestUsageLogFromService_KeepsUserBillingAndIPWithoutAdminCostFields(t *testing.T) {
	t.Parallel()

	ipAddress := "203.0.113.10"
	accountRateMultiplier := 1.5
	accountStatsCost := 0.21
	log := &service.UsageLog{
		RequestID:             "req_user_visible_billing",
		Model:                 "gpt-5.4",
		InputCost:             0.01,
		OutputCost:            0.02,
		CacheCreationCost:     0.03,
		CacheReadCost:         0.04,
		TotalCost:             0.10,
		ActualCost:            0.08,
		RateMultiplier:        0.8,
		IPAddress:             &ipAddress,
		AccountRateMultiplier: &accountRateMultiplier,
		AccountStatsCost:      &accountStatsCost,
	}

	userDTO := UsageLogFromService(log)
	require.Equal(t, 0.01, userDTO.InputCost)
	require.Equal(t, 0.02, userDTO.OutputCost)
	require.Equal(t, 0.03, userDTO.CacheCreationCost)
	require.Equal(t, 0.04, userDTO.CacheReadCost)
	require.Equal(t, 0.10, userDTO.TotalCost)
	require.Equal(t, 0.08, userDTO.ActualCost)
	require.Equal(t, 0.8, userDTO.RateMultiplier)
	require.NotNil(t, userDTO.IPAddress)
	require.Equal(t, ipAddress, *userDTO.IPAddress)

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "account_rate_multiplier")
	require.NotContains(t, string(userJSON), "account_stats_cost")
	require.NotContains(t, string(userJSON), "account_cost")
}

func TestUsageLogFromService_IncludesAdaptiveBillingMetadata(t *testing.T) {
	t.Parallel()

	adaptiveBaseCost := 0.8
	adaptiveManagementFeeCost := 0.12
	adaptiveParentGroupID := int64(101)
	routedGroupID := int64(202)
	adaptiveAttemptNo := 2
	adaptivePricingSnapshotID := "pricing-42"
	adaptiveReservationID := "reservation-7"
	adaptiveSettlementStatus := "captured"
	log := &service.UsageLog{
		RequestID:                 "req_adaptive",
		Model:                     "claude-opus-4-8",
		AdaptiveBaseCost:          &adaptiveBaseCost,
		AdaptiveManagementFeeCost: &adaptiveManagementFeeCost,
		AdaptiveParentGroupID:     &adaptiveParentGroupID,
		RoutedGroupID:             &routedGroupID,
		AdaptiveAttemptNo:         &adaptiveAttemptNo,
		AdaptivePricingSnapshotID: &adaptivePricingSnapshotID,
		AdaptiveReservationID:     &adaptiveReservationID,
		AdaptiveSettlementStatus:  &adaptiveSettlementStatus,
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	for _, usageDTO := range []*UsageLog{userDTO, &adminDTO.UsageLog} {
		require.NotNil(t, usageDTO.AdaptiveBaseCost)
		require.InDelta(t, adaptiveBaseCost, *usageDTO.AdaptiveBaseCost, 1e-12)
		require.NotNil(t, usageDTO.AdaptiveManagementFeeCost)
		require.InDelta(t, adaptiveManagementFeeCost, *usageDTO.AdaptiveManagementFeeCost, 1e-12)
		require.NotNil(t, usageDTO.AdaptiveParentGroupID)
		require.Equal(t, adaptiveParentGroupID, *usageDTO.AdaptiveParentGroupID)
		require.NotNil(t, usageDTO.RoutedGroupID)
		require.Equal(t, routedGroupID, *usageDTO.RoutedGroupID)
		require.NotNil(t, usageDTO.AdaptiveAttemptNo)
		require.Equal(t, adaptiveAttemptNo, *usageDTO.AdaptiveAttemptNo)
		require.NotNil(t, usageDTO.AdaptivePricingSnapshotID)
		require.Equal(t, adaptivePricingSnapshotID, *usageDTO.AdaptivePricingSnapshotID)
		require.NotNil(t, usageDTO.AdaptiveReservationID)
		require.Equal(t, adaptiveReservationID, *usageDTO.AdaptiveReservationID)
		require.NotNil(t, usageDTO.AdaptiveSettlementStatus)
		require.Equal(t, adaptiveSettlementStatus, *usageDTO.AdaptiveSettlementStatus)
	}

	userJSON, err := json.Marshal(userDTO)
	require.NoError(t, err)
	require.Contains(t, string(userJSON), `"adaptive_management_fee_cost":0.12`)
	require.Contains(t, string(userJSON), `"routed_group_id":202`)
	require.Contains(t, string(userJSON), `"adaptive_settlement_status":"captured"`)
}

func TestUsageLogFromService_FallsBackToLegacyModelWhenRequestedModelMissing(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID: "req_legacy",
		Model:     "claude-3",
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	require.Equal(t, "claude-3", userDTO.Model)
	require.Equal(t, "claude-3", adminDTO.Model)
}

func TestUsageLogFromService_IncludesImageBillingMetadataForUserAndAdmin(t *testing.T) {
	t.Parallel()

	imageSize := "4K"
	inputSize := "1024x1024"
	outputSize := "3840x2160"
	source := "output"
	log := &service.UsageLog{
		RequestID:          "req_image_metadata",
		Model:              "gpt-image-2",
		ImageCount:         2,
		ImageSize:          &imageSize,
		ImageInputSize:     &inputSize,
		ImageOutputSize:    &outputSize,
		ImageSizeSource:    &source,
		ImageSizeBreakdown: map[string]int{"4K": 2},
	}

	userDTO := UsageLogFromService(log)
	adminDTO := UsageLogFromServiceAdmin(log)

	for _, got := range []*UsageLog{userDTO, &adminDTO.UsageLog} {
		require.Equal(t, 2, got.ImageCount)
		require.NotNil(t, got.ImageSize)
		require.Equal(t, imageSize, *got.ImageSize)
		require.NotNil(t, got.ImageInputSize)
		require.Equal(t, inputSize, *got.ImageInputSize)
		require.NotNil(t, got.ImageOutputSize)
		require.Equal(t, outputSize, *got.ImageOutputSize)
		require.NotNil(t, got.ImageSizeSource)
		require.Equal(t, source, *got.ImageSizeSource)
		require.Equal(t, map[string]int{"4K": 2}, got.ImageSizeBreakdown)
	}
}

func TestUsageLogFromService_PreservesHistoricalMissingImageSize(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:  "req_legacy_image_missing_size",
		Model:      "gpt-image-2",
		ImageCount: 1,
		ImageSize:  nil,
	}

	dto := UsageLogFromService(log)
	require.Equal(t, 1, dto.ImageCount)
	require.Nil(t, dto.ImageSize)
	require.Nil(t, dto.ImageInputSize)
	require.Nil(t, dto.ImageOutputSize)
	require.Nil(t, dto.ImageSizeSource)
	require.Nil(t, dto.ImageSizeBreakdown)

	body, err := json.Marshal(dto)
	require.NoError(t, err)
	require.Contains(t, string(body), `"image_size":null`)
	require.NotContains(t, string(body), `"image_size":"2K"`)
}

func f64Ptr(value float64) *float64 {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func TestDeriveOutputTokensPerSecond_NormalWindow(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:    "req_rate_normal",
		Model:        "gpt-5.6-sol",
		RequestType:  service.RequestTypeStream,
		OutputTokens: 600,
		DurationMs:   intPtr(10000),
		FirstTokenMs: intPtr(4000),
	}

	rate := deriveOutputTokensPerSecond(log, service.RequestTypeStream)
	require.NotNil(t, rate)
	require.InDelta(t, 100.0, *rate, 1e-9)
}

func TestDeriveOutputTokensPerSecond_DegenerateWindowFallsBackToEndToEnd(t *testing.T) {
	t.Parallel()

	// Buffered/reasoning stream: visible output only surfaces at the very end,
	// so duration-first_token collapses to ~1ms and the window rate explodes.
	log := &service.UsageLog{
		RequestID:    "req_rate_degenerate",
		Model:        "glm-5.3",
		RequestType:  service.RequestTypeStream,
		OutputTokens: 433,
		DurationMs:   intPtr(14744),
		FirstTokenMs: intPtr(14743),
	}

	rate := deriveOutputTokensPerSecond(log, service.RequestTypeStream)
	require.NotNil(t, rate)
	// End-to-end throughput: 433 / 14.744s ≈ 29.37 tok/s, not 433000.
	require.InDelta(t, 29.37, *rate, 0.05)
	require.Less(t, *rate, outputRateWindowCap)
}

func TestDeriveOutputTokensPerSecond_NonStreamingReturnsNil(t *testing.T) {
	t.Parallel()

	log := &service.UsageLog{
		RequestID:    "req_rate_sync",
		Model:        "gpt-5.6-sol",
		RequestType:  service.RequestTypeSync,
		OutputTokens: 500,
		DurationMs:   intPtr(5000),
		FirstTokenMs: intPtr(1000),
	}

	require.Nil(t, deriveOutputTokensPerSecond(log, service.RequestTypeSync))
}

func TestDeriveOutputTokensPerSecond_MissingTimingReturnsNil(t *testing.T) {
	t.Parallel()

	base := service.UsageLog{
		RequestID:    "req_rate_missing",
		Model:        "gpt-5.6-sol",
		RequestType:  service.RequestTypeStream,
		OutputTokens: 500,
	}

	noDuration := base
	noDuration.FirstTokenMs = intPtr(1000)
	require.Nil(t, deriveOutputTokensPerSecond(&noDuration, service.RequestTypeStream))

	noFirstToken := base
	noFirstToken.DurationMs = intPtr(5000)
	require.Nil(t, deriveOutputTokensPerSecond(&noFirstToken, service.RequestTypeStream))

	zeroOutput := base
	zeroOutput.DurationMs = intPtr(5000)
	zeroOutput.FirstTokenMs = intPtr(1000)
	zeroOutput.OutputTokens = 0
	require.Nil(t, deriveOutputTokensPerSecond(&zeroOutput, service.RequestTypeStream))

	inverted := base
	inverted.OutputTokens = 500
	inverted.DurationMs = intPtr(1000)
	inverted.FirstTokenMs = intPtr(5000)
	require.Nil(t, deriveOutputTokensPerSecond(&inverted, service.RequestTypeStream))
}
