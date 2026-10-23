package handler

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"sync"
	"time"
)

type videoRouteBillingCache struct {
	testutil.StubGatewayCache
	mu       sync.Mutex
	bindings map[string]int64
	pending  map[string][]byte
	billed   map[string]bool
	loadErr  error
	storeErr error
}

func (s *videoRouteBillingCache) SetSessionAccountID(_ context.Context, groupID int64, key string, accountID int64, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bindings[fmt.Sprintf("%d:%s", groupID, key)] = accountID
	return nil
}

func (s *videoRouteBillingCache) GetSessionAccountID(_ context.Context, groupID int64, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.bindings[fmt.Sprintf("%d:%s", groupID, key)]
	if !ok {
		return 0, service.ErrStickySessionNotFound
	}
	return id, nil
}

func (s *videoRouteBillingCache) SetGrokVideoPendingBilling(_ context.Context, key string, payload []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storeErr != nil {
		return s.storeErr
	}
	s.pending[key] = append([]byte(nil), payload...)
	return nil
}

func (s *videoRouteBillingCache) GetGrokVideoPendingBilling(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.pending[key]...), s.loadErr
}

func (s *videoRouteBillingCache) ClaimGrokVideoBilled(_ context.Context, key string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.billed[key] {
		return false, nil
	}
	s.billed[key] = true
	return true, nil
}

type videoRouteResponseWriter struct {
	gin.ResponseWriter
	beforeWrite func()
}

func (w *videoRouteResponseWriter) Write(body []byte) (int, error) {
	if w.beforeWrite != nil {
		w.beforeWrite()
	}
	return w.ResponseWriter.Write(body)
}
