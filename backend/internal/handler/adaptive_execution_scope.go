package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func bindAdaptiveExecutionScope(c *gin.Context, group *service.Group, accountIDs []int64) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request = c.Request.WithContext(service.WithAdaptiveExecutionScope(c.Request.Context(), group, accountIDs))
}

func bindAdaptivePlanLeaf(c *gin.Context, plan *service.AdaptiveRoutePlan, group *service.Group) bool {
	if plan != nil && group != nil {
		for _, candidate := range plan.Candidates() {
			if candidate.LeafGroupID == group.ID {
				bindAdaptiveExecutionScope(c, group, candidate.CompatibleAccountIDs)
				return true
			}
		}
	}
	bindAdaptiveExecutionScope(c, group, nil)
	return false
}

func adaptiveRequestRequirements(c *gin.Context, protocol service.AdaptiveRouteProtocol, body []byte) (service.OpenAIEndpointCapability, bool) {
	switch protocol {
	case service.AdaptiveRouteProtocolOpenAIChat:
		return service.OpenAIEndpointCapabilityChatCompletions, false
	case service.AdaptiveRouteProtocolOpenAIResponses:
		compact := service.IsOpenAIResponsesCompactPath(c)
		nativeV2 := isBareOpenAIResponsesPath(c) && isOpenAIRemoteCompactionV2Request(body)
		image := service.IsExplicitImageGenerationIntent("/v1/responses", "", body)
		return openAIResponsesRequiredCapabilityForRequest(image, compact || nativeV2, service.PlatformOpenAI), compact
	case service.AdaptiveRouteProtocolOpenAIWebSocket:
		return service.OpenAIEndpointCapabilityResponses, false
	case service.AdaptiveRouteProtocolOpenAIMessages:
		return service.OpenAIEndpointCapabilityChatCompletions, false
	default:
		return "", false
	}
}
