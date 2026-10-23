package service

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// 叶子级会话粘性（adaptive 专用）。
//
// 背景：adaptive 每次请求都会重新规划并排序叶子，而规划只看倍率与健康分，
// **完全不记得这个会话上次落在哪个叶子**。于是健康分的轻微抖动就会把会话搬到
// 另一个叶子池，而上游提示缓存是按上游凭据隔离的 —— 换叶子等于把调用方的
// 整段上下文缓存扔掉（生产实测：同一 key 正常时 ~24 input token/请求，
// 被搬池后同一时段涨到 ~7,250）。
//
// 这里记住「会话 -> 上次成功使用的叶子」，让规划器把它排在候选首位。
// 粘性只影响**排序**，不改变可用性：一旦该叶子不可用或无法服务本次请求，
// 原有顺序照常生效。

const (
	// leafStickyTTL 是叶子粘性的存活时间。取值与上游提示缓存的有效期同量级：
	// 太短起不到保护作用，太长会让会话卡在一个已经劣化的池上。
	leafStickyTTL = 30 * time.Minute

	// leafStickyKeyPrefix 与账号粘性（"openai:"/"anthropic:" 等）区分开，
	// 避免覆盖 getSessionAccountID 使用的绑定槽位。
	leafStickyKeyPrefix = "adaptiveleaf:"
)

// leafStickyCache 是叶子粘性需要的最小缓存能力。GatewayCache 已满足。
type leafStickyCache interface {
	GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error)
	SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error
}

func leafStickyCacheKey(parentGroupID int64, sessionHash string) string {
	normalized := strings.TrimSpace(sessionHash)
	if normalized == "" || parentGroupID <= 0 {
		return ""
	}
	return leafStickyKeyPrefix + strconv.FormatInt(parentGroupID, 10) + ":" + normalized
}

// PreferredAdaptiveLeaf returns the leaf group this session last used under the
// parent, or 0 when there is no usable memory.
func PreferredAdaptiveLeaf(ctx context.Context, cache GatewayCache, parentGroupID int64, sessionHash string) int64 {
	if ctx == nil || cache == nil {
		return 0
	}
	key := leafStickyCacheKey(parentGroupID, sessionHash)
	if key == "" {
		return 0
	}
	reader, ok := cache.(leafStickyCache)
	if !ok {
		return 0
	}
	leafID, err := reader.GetSessionAccountID(ctx, 0, key)
	if err != nil || leafID <= 0 {
		return 0
	}
	return leafID
}

// RememberAdaptiveLeaf records the leaf a session actually used, so the next
// request in the same session starts from it instead of re-deriving an order
// that may point at a different upstream pool.
//
// Failures are intentionally swallowed: stickiness is a cache optimisation and
// must never fail a request.
func RememberAdaptiveLeaf(ctx context.Context, cache GatewayCache, parentGroupID int64, sessionHash string, leafGroupID int64) {
	if ctx == nil || cache == nil || leafGroupID <= 0 {
		return
	}
	key := leafStickyCacheKey(parentGroupID, sessionHash)
	if key == "" {
		return
	}
	writer, ok := cache.(leafStickyCache)
	if !ok {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = writer.SetSessionAccountID(cleanup, 0, key, leafGroupID, leafStickyTTL)
}

// ForgetAdaptiveLeaf drops the leaf memory for a session, used when the
// remembered leaf proved unusable so the next request is free to re-plan.
func ForgetAdaptiveLeaf(ctx context.Context, cache GatewayCache, parentGroupID int64, sessionHash string) {
	if ctx == nil || cache == nil {
		return
	}
	key := leafStickyCacheKey(parentGroupID, sessionHash)
	if key == "" {
		return
	}
	type deleter interface {
		DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error
	}
	d, ok := cache.(deleter)
	if !ok {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = d.DeleteSessionAccountID(cleanup, 0, key)
}
