package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveAccountGroupUpdateEmptyGroupsLeaveUnchanged(t *testing.T) {
	ids, bindGroups, bindIDs := resolveAccountGroupUpdate(nil, nil)
	require.Nil(t, ids)
	require.False(t, bindGroups)
	require.False(t, bindIDs)
}

func TestResolveAccountGroupUpdateExplicitEmptyGroupIDsClears(t *testing.T) {
	empty := []int64{}
	ids, bindGroups, bindIDs := resolveAccountGroupUpdate(nil, &empty)
	require.Empty(t, ids)
	require.False(t, bindGroups)
	require.True(t, bindIDs)
}

func TestShouldPreserveExistingAccountGroupsOnEmptyOverwrite(t *testing.T) {
	existing := []AccountGroup{{AccountID: 1, GroupID: 9, Priority: 1}}
	empty := []int64{}
	require.False(t, shouldPreserveExistingAccountGroups(existing, []int64{9}, nil, &empty))
	require.False(t, shouldPreserveExistingAccountGroups(existing, []int64{9}, []AccountGroup{}, &empty))
	require.False(t, shouldPreserveExistingAccountGroups(existing, []int64{9}, []AccountGroup{{GroupID: 3}}, nil))
	require.False(t, shouldPreserveExistingAccountGroups(nil, nil, nil, &empty))
	require.True(t, shouldPreserveExistingAccountGroups(existing, []int64{9}, nil, nil))
}

func TestShouldPreserveExistingAccountGroupsBulkEmptyIncoming(t *testing.T) {
	existing := []AccountGroup{{AccountID: 2, GroupID: 17, Priority: 1}}
	empty := []int64{}
	require.False(t, shouldPreserveExistingAccountGroups(existing, []int64{17}, nil, &empty))
	require.False(t, shouldPreserveExistingAccountGroups(existing, []int64{17}, []AccountGroup{}, &empty))
	require.False(t, shouldPreserveExistingAccountGroups(existing, []int64{17}, []AccountGroup{{GroupID: 85, Priority: 1}}, nil))
	require.False(t, shouldPreserveExistingAccountGroups(nil, nil, nil, &empty))
	_, bindGroups, bindIDs := resolveAccountGroupUpdate(nil, nil)
	require.False(t, bindGroups)
	require.False(t, bindIDs)
}

func TestResolveAccountGroupUpdateZeroGroupIDFallsBackToGroupIDs(t *testing.T) {
	ids := []int64{17, 142}
	got, bindGroups, bindIDs := resolveAccountGroupUpdate([]AccountGroup{{AccountID: 1, Priority: 1}}, &ids)
	require.Equal(t, []int64{17, 142}, got)
	require.False(t, bindGroups)
	require.True(t, bindIDs)
}

func TestResolveAccountGroupUpdateZeroGroupIDWithoutGroupIDsLeavesUnchanged(t *testing.T) {
	got, bindGroups, bindIDs := resolveAccountGroupUpdate([]AccountGroup{{AccountID: 1, Priority: 1}}, nil)
	require.Nil(t, got)
	require.False(t, bindGroups)
	require.False(t, bindIDs)
}

func TestAccountGroupUnmarshalJSONAcceptsSnakeCase(t *testing.T) {
	var group AccountGroup
	require.NoError(t, json.Unmarshal([]byte(`{"account_id":2624,"group_id":17,"priority":2}`), &group))
	require.Equal(t, int64(2624), group.AccountID)
	require.Equal(t, int64(17), group.GroupID)
	require.Equal(t, 2, group.Priority)
}

func TestAccountGroupUnmarshalJSONAcceptsCamelCaseCacheKeys(t *testing.T) {
	var group AccountGroup
	require.NoError(t, json.Unmarshal([]byte(`{"AccountID":9,"GroupID":85,"Priority":1}`), &group))
	require.Equal(t, int64(9), group.AccountID)
	require.Equal(t, int64(85), group.GroupID)
	require.Equal(t, 1, group.Priority)
}

func TestAccountGroupMarshalJSONKeepsCamelCaseCacheKeys(t *testing.T) {
	raw, err := json.Marshal(AccountGroup{AccountID: 9, GroupID: 85, Priority: 1})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"AccountID":9`)
	require.Contains(t, string(raw), `"GroupID":85`)
	require.NotContains(t, string(raw), `"group_id"`)
}
