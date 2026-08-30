package service

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

func openAIStreamFailureShouldCooldown(message string) bool {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "cyber") || strings.Contains(normalized, "context_length") {
		return false
	}
	return strings.Contains(normalized, "stalled") && strings.Contains(normalized, "no real data")
}

func (s *OpenAIGatewayService) openAIStreamFailureCooldownHasAlternate(ctx context.Context, c *gin.Context, current *Account, requestedModel string) bool {
	if s == nil || current == nil {
		return false
	}
	groupID := currentPrimaryGroupID(c, current)
	excluded := map[int64]struct{}{current.ID: {}}
	account, err := s.SelectAccountForModelWithExclusions(ctx, groupID, "", requestedModel, excluded)
	return err == nil && account != nil
}

func currentPrimaryGroupID(c *gin.Context, current *Account) *int64 {
	if apiKey := getAPIKeyFromContext(c); apiKey != nil && apiKey.GroupID != nil {
		return apiKey.GroupID
	}
	if current == nil || len(current.GroupIDs) == 0 {
		return nil
	}
	groupID := current.GroupIDs[0]
	return &groupID
}
