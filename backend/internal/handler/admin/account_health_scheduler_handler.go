package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SetHealthScheduler attaches the built-in account health scheduler.
func (h *AccountHandler) SetHealthScheduler(s *service.AccountHealthScheduler) {
	h.healthScheduler = s
}

// GetHealthSchedulerPolicy returns the built-in health scheduler policy.
// GET /api/v1/admin/accounts/health-scheduler/settings
func (h *AccountHandler) GetHealthSchedulerPolicy(c *gin.Context) {
	if h.healthScheduler == nil {
		response.Error(c, http.StatusServiceUnavailable, "health scheduler not available")
		return
	}
	response.Success(c, h.healthScheduler.GetPolicy(c.Request.Context()))
}

// UpdateHealthSchedulerPolicy updates the built-in health scheduler policy.
// PUT /api/v1/admin/accounts/health-scheduler/settings
func (h *AccountHandler) UpdateHealthSchedulerPolicy(c *gin.Context) {
	if h.healthScheduler == nil {
		response.Error(c, http.StatusServiceUnavailable, "health scheduler not available")
		return
	}
	var req service.AccountHealthSchedulerPolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	updated, err := h.healthScheduler.UpdatePolicy(c.Request.Context(), req)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, updated)
}
