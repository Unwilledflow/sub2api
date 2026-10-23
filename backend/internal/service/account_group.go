package service

import (
	"encoding/json"
	"fmt"
	"time"
)

type AccountGroup struct {
	AccountID int64
	GroupID   int64
	Priority  int
	CreatedAt time.Time

	Account *Account
	Group   *Group
}

// UnmarshalJSON accepts both the scheduler-cache camelCase keys (AccountID/GroupID)
// and the admin UI snake_case keys (account_id/group_id). The struct itself has no
// json tags so existing Redis snapshots keep marshaling as AccountID/GroupID.
func (g *AccountGroup) UnmarshalJSON(data []byte) error {
	if g == nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("admin UI snake_case keys: %w", err)
	}
	decodeInt := func(keys ...string) int64 {
		for _, key := range keys {
			msg, ok := raw[key]
			if !ok {
				continue
			}
			var n int64
			if err := json.Unmarshal(msg, &n); err == nil {
				return n
			}
		}
		return 0
	}
	decodeTime := func(keys ...string) time.Time {
		for _, key := range keys {
			msg, ok := raw[key]
			if !ok {
				continue
			}
			var t time.Time
			if err := json.Unmarshal(msg, &t); err == nil {
				return t
			}
		}
		return time.Time{}
	}
	g.AccountID = decodeInt("account_id", "AccountID")
	g.GroupID = decodeInt("group_id", "GroupID")
	g.Priority = int(decodeInt("priority", "Priority"))
	g.CreatedAt = decodeTime("created_at", "CreatedAt")
	return nil
}
