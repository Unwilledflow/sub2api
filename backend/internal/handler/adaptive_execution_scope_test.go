package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestAdaptiveDisabledParentCannotBecomeOrdinaryAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	id := int64(9)
	key := &service.APIKey{GroupID: &id, Group: &service.Group{ID: id, Platform: service.PlatformAdaptive}, User: &service.User{ID: 1}}
	openai := &OpenAIGatewayHandler{cfg: &config.Config{}}
	session, owned := openai.startAdaptiveOpenAISession(context.Background(), c, key, "gpt-5.1", service.AdaptiveRouteProtocolOpenAIChat, true, nil, nil)
	require.True(t, owned)
	require.NotNil(t, session)
	require.Empty(t, session.Attempts)
	native := &GatewayHandler{cfg: &config.Config{}}
	require.Same(t, key, native.startGatewayAdaptiveIfParent(context.Background(), c, key, "claude", service.AdaptiveRouteProtocolAnthropicMessages, nil, nil))
	// Deny-all execution scope must be inherited by scheduler contexts.
	_, err := (&service.GatewayService{}).SelectAccountForModelWithExclusions(c.Request.Context(), &id, "", "claude", nil)
	require.ErrorIs(t, err, service.ErrNoAvailableAccounts)
}

type nativeAdaptiveGroupRepo struct {
	service.GroupRepository
	group *service.Group
}

func (r nativeAdaptiveGroupRepo) GetByIDLite(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}

func TestAdaptiveNativeLeafTransitionPreservesSettlementFence(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	billing := &service.AdaptiveBillingContext{
		ReservationID: "2fa97db0-6c9f-48d0-a1fb-a526521dd3a0", LogicalRequestID: "native-fallback", OwnerID: "gateway-1",
		FencingToken: 1, RowVersion: 1, ParentGroupID: 108, LeafGroupID: 85, AttemptNo: 1,
		PricingSnapshotID: "pricing-v1", FundingSource: service.UsageReservationFundingBalance,
		HeldBaseCost: decimal.NewFromInt(5), HeldManagementFee: decimal.RequireFromString("0.75"), HeldTotal: decimal.RequireFromString("5.75"),
	}
	session := &gatewayAdaptiveSession{Billing: billing, Plan: adaptiveTestPlan(85, 34), LeafGroupIDs: []int64{85, 34}}
	c.Set(ginKeyGatewayAdaptiveSession, session)
	repository := &adaptiveAttemptTransitionRepository{}
	h := &GatewayHandler{
		adaptiveBilling: service.NewAdaptiveBillingCoordinator(repository, nil),
		apiKeyService:   service.NewAPIKeyService(nil, nil, nativeAdaptiveGroupRepo{group: &service.Group{ID: 34, Platform: service.PlatformAnthropic}}, nil, nil, nil, nil),
	}
	next := h.advanceGatewayAdaptiveLeaf(context.Background(), c, &service.APIKey{}, nil)
	require.NotNil(t, next)
	require.Equal(t, int64(34), *next.GroupID)
	require.NotNil(t, repository.failedCmd)
	require.NotNil(t, repository.inFlightCmd)
	require.Equal(t, int64(34), billing.LeafGroupID)
	require.Equal(t, 2, billing.AttemptNo)
	require.Equal(t, int64(3), billing.RowVersion)
	require.NoError(t, billing.ValidateForSettlement())
}

func TestAdaptiveNativeFinishReleasesOnlyUnsubmittedHold(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		session := &gatewayAdaptiveSession{UsageSubmitted: submitted, Billing: &service.AdaptiveBillingContext{ReservationID: "reservation", LogicalRequestID: "request", OwnerID: "owner", FencingToken: 1, RowVersion: 1, ParentGroupID: 108, PricingSnapshotID: "pricing"}}
		c.Set(ginKeyGatewayAdaptiveSession, session)
		repo := &adaptiveHeartbeatRepository{}
		h := &GatewayHandler{adaptiveBilling: service.NewAdaptiveBillingCoordinator(repo, nil)}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		h.finishGatewayAdaptiveRequest(canceled, c, nil)
		if submitted {
			require.Equal(t, 0, repo.releaseCalls)
		} else {
			require.Equal(t, 1, repo.releaseCalls)
		}
	}
}

func TestAdaptiveRequestRequirements(t *testing.T) {
	for _, tc := range []struct {
		path       string
		body       string
		capability service.OpenAIEndpointCapability
		compact    bool
	}{
		{"/v1/responses", `{}`, service.OpenAIEndpointCapabilityChatCompletions, false},
		{"/v1/responses/compact", `{}`, service.OpenAIEndpointCapabilityResponses, true},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", tc.path, nil)
		capability, compact := adaptiveRequestRequirements(c, service.AdaptiveRouteProtocolOpenAIResponses, []byte(tc.body))
		require.Equal(t, tc.capability, capability)
		require.Equal(t, tc.compact, compact)
	}
}
