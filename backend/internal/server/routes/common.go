package routes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const defaultReadinessTimeout = 2 * time.Second

// ReadinessCheck is a single dependency probe used by /ready.
type ReadinessCheck struct {
	Name  string
	Check func(context.Context) error
}

// CommonRouteOptions configures unauthenticated operational endpoints.
type CommonRouteOptions struct {
	ReadinessChecks  []ReadinessCheck
	ReadinessTimeout time.Duration
}

// RegisterCommonRoutes registers unauthenticated operational endpoints.
func RegisterCommonRoutes(r *gin.Engine) {
	RegisterCommonRoutesWithOptions(r, CommonRouteOptions{})
}

// RegisterCommonRoutesWithOptions registers unauthenticated operational endpoints.
func RegisterCommonRoutesWithOptions(r *gin.Engine, options CommonRouteOptions) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.GET("/ready", func(c *gin.Context) {
		timeout := options.ReadinessTimeout
		if timeout <= 0 {
			timeout = defaultReadinessTimeout
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		checks := gin.H{}
		ready := true
		for _, check := range options.ReadinessChecks {
			if check.Name == "" || check.Check == nil {
				continue
			}
			if err := check.Check(ctx); err != nil {
				ready = false
				checks[check.Name] = gin.H{"status": "error"}
				continue
			}
			checks[check.Name] = gin.H{"status": "ok"}
		}

		if !ready {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "degraded",
				"checks": checks,
			})
			return
		}
		if len(checks) > 0 {
			c.JSON(http.StatusOK, gin.H{
				"status": "ok",
				"checks": checks,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// /readyz mirrors /ready for Kubernetes-style probes on the same checks.
	// Readiness is separate from liveness so a rollout can wait for shared
	// dependencies before adding a replica back behind the proxy.
	readyzHandler := func(c *gin.Context) {
		if len(options.ReadinessChecks) == 0 {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		timeout := options.ReadinessTimeout
		if timeout <= 0 {
			timeout = defaultReadinessTimeout
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		for _, check := range options.ReadinessChecks {
			if check.Check == nil {
				continue
			}
			if err := check.Check(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	}
	r.GET("/readyz", readyzHandler)

	// Claude Code telemetry can be ignored with a fast 200 response.
	r.POST("/api/event_logging/batch", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Setup status endpoint used after first-run setup completes.
	r.GET("/setup/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"needs_setup": false,
				"step":        "completed",
			},
		})
	})
}

func DatabaseReadinessCheck(db *sql.DB) ReadinessCheck {
	return ReadinessCheck{
		Name: "database",
		Check: func(ctx context.Context) error {
			if db == nil {
				return errors.New("database client is not configured")
			}
			return db.PingContext(ctx)
		},
	}
}

func RedisReadinessCheck(client *redis.Client) ReadinessCheck {
	return ReadinessCheck{
		Name: "redis",
		Check: func(ctx context.Context) error {
			if client == nil {
				return errors.New("redis client is not configured")
			}
			return client.Ping(ctx).Err()
		},
	}
}
