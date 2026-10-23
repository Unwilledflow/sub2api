//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAdaptiveExecutionScopeSimpleAndSticky(t *testing.T) {
	group := &Group{ID: 9, Platform: PlatformOpenAI}
	accounts := []Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{9}},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{9}},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, GroupIDs: []int64{10}},
	}
	repo := &thresholdSelectionAccountRepoStub{accounts: accounts}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	openai := &OpenAIGatewayService{accountRepo: repo, cfg: cfg}
	gateway := &GatewayService{accountRepo: repo, cfg: cfg}
	ctx := WithAdaptiveExecutionScope(context.Background(), group, []int64{1, 3})
	list, err := openai.listSchedulableAccounts(ctx, &group.ID, PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, int64(1), list[0].ID)
	native, _, err := gateway.listSchedulableAccounts(ctx, &group.ID, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Len(t, native, 1)
	for i := range accounts {
		selected := openai.recheckSelectedOpenAIAccountFromDB(ctx, &accounts[i], &group.ID, PlatformOpenAI, "gpt-5.1", false, "")
		require.Equal(t, i == 0, selected != nil)
		require.Equal(t, i == 0, gateway.isAccountSchedulableForModelSelection(ctx, &accounts[i], "gpt-5.1"))
	}
	// Exercise the actual sticky scheduler, not only the eligibility helper.
	openai.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
	openai.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"sticky": 2}}
	openai.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{})
	selected, err := openai.SelectAccountWithLoadAwareness(ctx, &group.ID, "sticky", "gpt-5.1", nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, int64(1), selected.Account.ID)
	if selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
	ordinary, err := openai.listSchedulableAccounts(context.Background(), &group.ID, PlatformOpenAI)
	require.NoError(t, err)
	require.Len(t, ordinary, 3)
	denied := WithAdaptiveExecutionScope(ctx, group, nil)
	list, err = openai.listSchedulableAccounts(denied, &group.ID, PlatformOpenAI)
	require.NoError(t, err)
	require.Empty(t, list)
	other := int64(10)
	require.False(t, adaptiveExecutionGroupAllowed(ctx, &other))
	_, _, err = gateway.resolveGatewayGroup(ctx, &other)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}
