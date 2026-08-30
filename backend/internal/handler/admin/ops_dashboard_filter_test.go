package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseOpsDashboardFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("parses range and group filter", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("GET", "/?time_range=30m&platform=openai&group_id=42&mode=raw", nil)

		filter, err := parseOpsDashboardFilter(ctx)
		require.NoError(t, err)
		require.Equal(t, "openai", filter.Platform)
		require.Equal(t, int64(42), *filter.GroupID)
		require.False(t, filter.StartTime.IsZero())
		require.False(t, filter.EndTime.IsZero())
		require.Equal(t, service.OpsQueryModeRaw, filter.QueryMode)
	})

	t.Run("rejects invalid group id", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("GET", "/?group_id=zero", nil)

		_, err := parseOpsDashboardFilter(ctx)
		require.EqualError(t, err, "Invalid group_id")
	})
}
