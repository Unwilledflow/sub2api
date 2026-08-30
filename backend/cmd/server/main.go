package main

//go:generate go run github.com/google/wire/cmd/wire

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/setup"
	"github.com/Wei-Shaw/sub2api/internal/web"
	"github.com/Wei-Shaw/sub2api/migrations"

	"github.com/gin-gonic/gin"
)

//go:embed VERSION
var embeddedVersion string

// Build-time variables (can be set by ldflags)
var (
	Version   = ""
	Commit    = "unknown"
	Date      = "unknown"
	BuildType = "source" // "source" for manual builds, "release" for CI builds (set by ldflags)
)

func init() {
	// 如果 Version 已通过 ldflags 注入（例如 -X main.Version=...），则不要覆盖。
	if strings.TrimSpace(Version) != "" {
		return
	}

	// 默认从 embedded VERSION 文件读取版本号（编译期打包进二进制）。
	Version = strings.TrimSpace(embeddedVersion)
	if Version == "" {
		Version = "0.0.0-dev"
	}
}

// initLogger configures the default slog handler based on gin.Mode().
// In non-release mode, Debug level logs are enabled.

func dumpMigrationChecksumsIfRequested() {
	if os.Getenv("DUMP_MIGRATION_CHECKSUMS") == "" {
		return
	}
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := migrations.FS.ReadFile(e.Name())
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(data)
		if e.Name() == "001_init.sql" || e.Name() == "002_account_type_migration.sql" {
			fmt.Printf("EMBED|%s|%x\n", e.Name(), sum)
		}
	}
	os.Exit(0)
}

func main() {
	dumpMigrationChecksumsIfRequested()
	logger.InitBootstrap()
	defer logger.Sync()

	// Parse command line flags
	setupMode := flag.Bool("setup", false, "Run setup wizard in CLI mode")
	showVersion := flag.Bool("version", false, "Show version information")
	migrateOnly := flag.Bool("migrate-only", false, "Apply database migrations and exit")
	checkMigrations := flag.Bool("check-migrations", false, "Validate database migrations without changing schema and exit")
	flag.Parse()
	if *migrateOnly && *checkMigrations {
		log.Fatal("-migrate-only and -check-migrations are mutually exclusive")
	}

	if *showVersion {
		log.Printf("Sub2API %s (commit: %s, built: %s)\n", Version, Commit, Date)
		return
	}

	// CLI setup mode
	if *setupMode {
		if err := setup.RunCLI(); err != nil {
			log.Fatalf("Setup failed: %v", err)
		}
		return
	}
	if *migrateOnly || *checkMigrations {
		runMigrationCommand(*checkMigrations)
		return
	}

	// Check if setup is needed
	if setup.NeedsSetup() {
		// Check if auto-setup is enabled (for Docker deployment)
		if setup.AutoSetupEnabled() {
			log.Println("Auto setup mode enabled...")
			if err := setup.AutoSetupFromEnv(); err != nil {
				log.Fatalf("Auto setup failed: %v", err)
			}
			// Continue to main server after auto-setup
		} else {
			log.Println("First run detected, starting setup wizard...")
			runSetupServer()
			return
		}
	}

	// Normal server mode
	runMainServer()
}

func runMigrationCommand(validateOnly bool) {
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := repository.RunConfiguredMigrations(ctx, cfg, validateOnly); err != nil {
		if validateOnly {
			log.Fatalf("Migration validation failed: %v", err)
		}
		log.Fatalf("Database migration failed: %v", err)
	}
	if validateOnly {
		log.Println("Database migrations are current")
		return
	}
	log.Println("Database migrations applied successfully")
}

func runSetupServer() {
	r := gin.New()
	r.Use(middleware.Recovery())
	r.Use(middleware.CORS(config.CORSConfig{}))
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: config.DefaultCSPPolicy}, nil))

	// Register setup routes
	setup.RegisterRoutes(r)

	// Serve embedded frontend if available
	if web.HasEmbeddedFrontend() {
		r.Use(web.ServeEmbeddedFrontend())
	}

	// Bind setup wizard to loopback by default to prevent remote takeover during first install.
	// Override with SERVER_HOST only when also setting SETUP_TOKEN for authenticated remote setup.
	addr := config.GetSetupServerAddress()
	if token := strings.TrimSpace(os.Getenv("SETUP_TOKEN")); token == "" {
		log.Printf("Setup wizard available at http://%s (loopback default; set SETUP_TOKEN to allow remote access)", addr)
	} else {
		log.Printf("Setup wizard available at http://%s (SETUP_TOKEN required for mutating endpoints)", addr)
	}
	log.Println("Complete the setup wizard to configure Sub2API")

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
		Protocols:         protocols,
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Failed to start setup server: %v", err)
	}
}

func runMainServer() {
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if err := logger.Init(logger.OptionsFromConfig(cfg.Log)); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	if cfg.RunMode == config.RunModeSimple {
		log.Println("⚠️  WARNING: Running in SIMPLE mode - billing and quota checks are DISABLED")
	}

	buildInfo := handler.BuildInfo{
		Version:   Version,
		BuildType: BuildType,
	}

	app, err := initializeApplication(buildInfo)
	if err != nil {
		log.Fatalf("Failed to initialize application: %v", err)
	}
	defer app.Cleanup()
	profiler, err := startPprofServerFromEnv()
	if err != nil {
		log.Fatalf("Failed to start pprof diagnostics: %v", err)
	}
	if profiler != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := profiler.Shutdown(ctx); err != nil {
				log.Printf("Failed to stop pprof diagnostics: %v", err)
			}
		}()
	}
	if app.PluginManager != nil {
		if err := app.PluginManager.Start(context.Background()); err != nil {
			log.Printf("Plugin manager started in degraded state: %v", err)
		}
	}
	if app.PromptAudit != nil {
		if err := app.PromptAudit.Start(context.Background()); err != nil {
			// Startup continues so unrelated APIs stay up. Fail-closed (unavailable)
			// applies only when a persisted blocking policy was observed; without
			// blocking intent, Prompt Audit stays ModeOff so the gateway remains
			// usable and administrators can still disable the feature (#4560).
			log.Printf("Prompt Audit started in degraded state: %v", err)
		}
	}

	profilingServer, err := startProfilingServer()
	if err != nil {
		log.Fatalf("Failed to start profiling server: %v", err)
	}

	// 启动服务器
	go func() {
		if err := app.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	log.Printf("Server started on %s", app.Server.Addr)

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

// shutdown 超时可配置（默认 30s），给在途流式请求更多排空时间，
	// 避免 5s 强切导致长流中断与漏计费。
	shutdownTimeout := 30 * time.Second
	if cfg.Server.ShutdownTimeoutSeconds > 0 {
		shutdownTimeout = time.Duration(cfg.Server.ShutdownTimeoutSeconds) * time.Second
	} else if v := shutdownTimeoutFromEnv(os.Getenv("SERVER_SHUTDOWN_TIMEOUT_SECONDS")); v > 0 {
		shutdownTimeout = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := app.Server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}
	if profilingServer != nil {
		if err := profilingServer.Shutdown(ctx); err != nil {
			log.Printf("Profiling server forced to shutdown: %v", err)
		}
	}

	log.Println("Server exited")
}

const (
	defaultShutdownTimeout = 30 * time.Second
	minShutdownTimeout     = 5 * time.Second
	maxShutdownTimeout     = 10 * time.Minute
)

func shutdownTimeoutFromEnv(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultShutdownTimeout
	}
	parsed, err := time.ParseDuration(raw + "s")
	if err != nil || parsed < minShutdownTimeout || parsed > maxShutdownTimeout {
		return defaultShutdownTimeout
	}
	return parsed
}
