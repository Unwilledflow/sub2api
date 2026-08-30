package service

import "context"

// OpenAIPool5xxCounterCache tracks transient upstream failures per pool account.
type OpenAIPool5xxCounterCache interface {
	ObserveOpenAIPool5xxFailure(ctx context.Context, accountID int64, windowSeconds, sampleIntervalSeconds int) (count int64, sampled bool, err error)
	ResetOpenAIPool5xxCount(ctx context.Context, accountID int64) error
	ClearOpenAIPool5xxState(ctx context.Context, accountID int64) error
}
