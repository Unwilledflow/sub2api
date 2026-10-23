package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type adaptiveExecutionScopeKey struct{}
type adaptiveExecutionScope struct {
	groupID    int64
	accountIDs map[int64]struct{}
}

// WithAdaptiveExecutionScope freezes the planned leaf's account boundary for
// every scheduler path, including sticky and simple-mode selection. An empty
// scope deliberately denies execution (an unavailable Adaptive parent).
func WithAdaptiveExecutionScope(ctx context.Context, group *Group, accountIDs []int64) context.Context {
	scope := adaptiveExecutionScope{accountIDs: make(map[int64]struct{}, len(accountIDs))}
	if group != nil {
		scope.groupID = group.ID
	}
	for _, id := range accountIDs {
		scope.accountIDs[id] = struct{}{}
	}
	ctx = context.WithValue(ctx, adaptiveExecutionScopeKey{}, scope)
	ctx = context.WithValue(ctx, ctxkey.Group, group)
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, "")
	platform := ""
	if group != nil {
		platform = group.Platform
	}
	return context.WithValue(ctx, ctxkey.ResolvedTargetPlatform, platform)
}

func adaptiveExecutionAccountAllowed(ctx context.Context, account *Account) bool {
	scope, ok := ctx.Value(adaptiveExecutionScopeKey{}).(adaptiveExecutionScope)
	if !ok {
		return true
	}
	if account == nil || scope.groupID <= 0 {
		return false
	}
	_, allowed := scope.accountIDs[account.ID]
	return allowed && openAIStickyAccountMatchesGroup(account, &scope.groupID)
}

func adaptiveExecutionGroupAllowed(ctx context.Context, groupID *int64) bool {
	scope, ok := ctx.Value(adaptiveExecutionScopeKey{}).(adaptiveExecutionScope)
	return !ok || (groupID != nil && *groupID == scope.groupID && scope.groupID > 0 && len(scope.accountIDs) > 0)
}
