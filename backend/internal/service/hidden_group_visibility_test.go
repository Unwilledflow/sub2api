package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupHiddenFromEndUsers(t *testing.T) {
	t.Parallel()

	require.False(t, (*Group)(nil).HiddenFromEndUsers())
	require.False(t, (&Group{}).HiddenFromEndUsers())
	require.False(t, (&Group{VisibilityScope: GroupVisibilityPublic}).HiddenFromEndUsers())
	require.True(t, (&Group{VisibilityScope: GroupVisibilityHidden}).HiddenFromEndUsers())
	require.False(t, (&Group{VisibilityScope: GroupVisibilityRestricted}).HiddenFromEndUsers())
	// 空值按列默认值 public 处理。
	require.False(t, (&Group{VisibilityScope: "bogus"}).HiddenFromEndUsers())
}

func TestGroupVisibleToUserAllowsAdminOnlyForHiddenGroups(t *testing.T) {
	t.Parallel()

	hidden := &Group{ID: 7, VisibilityScope: GroupVisibilityHidden}
	visible := &Group{ID: 8, VisibilityScope: GroupVisibilityPublic}
	user := &User{Role: RoleUser}
	admin := &User{Role: RoleAdmin}

	require.False(t, hidden.VisibleToUser(nil))
	require.False(t, hidden.VisibleToUser(user))
	require.True(t, hidden.VisibleToUser(admin))
	require.True(t, visible.VisibleToUser(user))
	require.True(t, visible.VisibleToUser(nil))
}

// restricted 是「仅指定用户可见」：名单内可见可绑，名单外一律不可见（管理员除外）。
func TestGroupRestrictedVisibleOnlyToGrantedUsers(t *testing.T) {
	t.Parallel()

	group := &Group{ID: 20, VisibilityScope: GroupVisibilityRestricted}
	granted := &User{ID: 1, Role: RoleUser, AllowedGroups: []int64{20}}
	other := &User{ID: 2, Role: RoleUser, AllowedGroups: []int64{21}}
	ungranted := &User{ID: 3, Role: RoleUser}
	admin := &User{ID: 4, Role: RoleAdmin}

	require.True(t, group.VisibleToUser(granted))
	require.False(t, group.VisibleToUser(other))
	require.False(t, group.VisibleToUser(ungranted))
	require.True(t, group.VisibleToUser(admin))
	require.False(t, group.VisibleToUser(nil))

	// 名单内的用户即便未开启 restrict_public_groups 也能绑定受限分组；
	// 名单外的用户被 VisibleToUser 拦下（CanBindGroup 单独看是放行的）。
	require.True(t, granted.CanBindVisibleGroup(group))
	require.False(t, other.CanBindVisibleGroup(group))
	require.False(t, ungranted.CanBindVisibleGroup(group))
	require.True(t, admin.CanBindVisibleGroup(group))
}

func TestCanUserBindGroupInternalHidesNonVisibleGroups(t *testing.T) {
	t.Parallel()

	svc := &APIKeyService{}
	user := &User{ID: 1, Role: RoleUser}
	admin := &User{ID: 2, Role: RoleAdmin}
	hidden := &Group{ID: 11, VisibilityScope: GroupVisibilityHidden, SubscriptionType: SubscriptionTypeStandard}
	public := &Group{ID: 12, VisibilityScope: GroupVisibilityPublic, SubscriptionType: SubscriptionTypeStandard}

	require.False(t, svc.canUserBindGroupInternal(user, hidden, nil))
	require.True(t, svc.canUserBindGroupInternal(admin, hidden, nil))
	require.True(t, svc.canUserBindGroupInternal(user, public, nil))
	require.False(t, svc.canUserBindGroupInternal(nil, public, nil))
}
