package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type cacheInvalidationReservationRepoStub struct {
	UsageBillingReservationRepository
	result       *UsageReservationResult
	err          error
	reserveCalls int
	captureCalls int
	releaseCalls int
	renewCalls   int
}

func (s *cacheInvalidationReservationRepoStub) Reserve(context.Context, *UsageReservationReserveCommand) (*UsageReservationResult, error) {
	s.reserveCalls++
	return s.result, s.err
}

func (s *cacheInvalidationReservationRepoStub) Capture(context.Context, *UsageReservationCaptureCommand) (*UsageReservationResult, error) {
	s.captureCalls++
	return s.result, s.err
}

func (s *cacheInvalidationReservationRepoStub) Release(context.Context, *UsageReservationReleaseCommand) (*UsageReservationResult, error) {
	s.releaseCalls++
	return s.result, s.err
}

func (s *cacheInvalidationReservationRepoStub) Renew(context.Context, *UsageReservationRenewCommand) (*UsageReservationResult, error) {
	s.renewCalls++
	return s.result, s.err
}

type adaptiveReservationInvalidatorStub struct {
	calls       int
	ctxErr      error
	result      *UsageReservationResult
	returnError error
}

func (s *adaptiveReservationInvalidatorStub) InvalidateAdaptiveReservation(ctx context.Context, result *UsageReservationResult) error {
	s.calls++
	s.ctxErr = ctx.Err()
	s.result = result
	return s.returnError
}

func TestCacheInvalidatingUsageBillingReservationRepository_InvalidatesFinancialLifecycle(t *testing.T) {
	result := &UsageReservationResult{Reservation: &UsageBillingReservation{ID: "reservation-1", UserID: 7, APIKeyID: 9}}
	delegate := &cacheInvalidationReservationRepoStub{result: result}
	invalidator := &adaptiveReservationInvalidatorStub{}
	repo := NewCacheInvalidatingUsageBillingReservationRepository(delegate, invalidator)

	_, err := repo.Reserve(context.Background(), &UsageReservationReserveCommand{})
	require.NoError(t, err)
	_, err = repo.Capture(context.Background(), &UsageReservationCaptureCommand{})
	require.NoError(t, err)
	_, err = repo.Release(context.Background(), &UsageReservationReleaseCommand{})
	require.NoError(t, err)
	_, err = repo.Renew(context.Background(), &UsageReservationRenewCommand{})
	require.NoError(t, err)

	require.Equal(t, 4, invalidator.calls)
	require.Same(t, result, invalidator.result)
	require.Equal(t, 1, delegate.reserveCalls)
	require.Equal(t, 1, delegate.captureCalls)
	require.Equal(t, 1, delegate.releaseCalls)
	require.Equal(t, 1, delegate.renewCalls)
}

func TestCacheInvalidatingUsageBillingReservationRepository_DetachesCancellationAndPreservesCommit(t *testing.T) {
	result := &UsageReservationResult{Reservation: &UsageBillingReservation{ID: "reservation-1", UserID: 7, APIKeyID: 9}}
	delegate := &cacheInvalidationReservationRepoStub{result: result}
	invalidator := &adaptiveReservationInvalidatorStub{returnError: errors.New("redis unavailable")}
	repo := NewCacheInvalidatingUsageBillingReservationRepository(delegate, invalidator)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := repo.Reserve(ctx, &UsageReservationReserveCommand{})

	require.NoError(t, err, "post-commit cache errors must not turn a committed hold into a failed authorization")
	require.Same(t, result, got)
	require.NoError(t, invalidator.ctxErr)
	require.Equal(t, 1, invalidator.calls)
}

func TestCacheInvalidatingUsageBillingReservationRepository_SkipsFailedMutation(t *testing.T) {
	delegateErr := errors.New("transaction rolled back")
	delegate := &cacheInvalidationReservationRepoStub{err: delegateErr}
	invalidator := &adaptiveReservationInvalidatorStub{}
	repo := NewCacheInvalidatingUsageBillingReservationRepository(delegate, invalidator)

	_, err := repo.Release(context.Background(), &UsageReservationReleaseCommand{})

	require.ErrorIs(t, err, delegateErr)
	require.Zero(t, invalidator.calls)
}

func TestCacheInvalidatingUsageBillingReservationRepository_HealsIdempotentReplay(t *testing.T) {
	result := &UsageReservationResult{
		Applied:     false,
		Reservation: &UsageBillingReservation{ID: "reservation-1", UserID: 7, APIKeyID: 9},
	}
	invalidator := &adaptiveReservationInvalidatorStub{}
	repo := NewCacheInvalidatingUsageBillingReservationRepository(
		&cacheInvalidationReservationRepoStub{result: result}, invalidator,
	)

	_, err := repo.Capture(context.Background(), &UsageReservationCaptureCommand{})

	require.NoError(t, err)
	require.Equal(t, 1, invalidator.calls, "a replay should retry post-commit cache convergence")
}
