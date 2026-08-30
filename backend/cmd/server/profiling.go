package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	profilingEnabledEnv  = "SUB2API_PPROF_ENABLED"
	profilingPortEnv     = "SUB2API_PPROF_PORT"
	defaultProfilingPort = 6060
)

type profilingSettings struct {
	enabled bool
	port    int
}

func (s profilingSettings) address() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(s.port))
}

func profilingSettingsFromEnv() (profilingSettings, error) {
	settings := profilingSettings{port: defaultProfilingPort}

	if raw := strings.TrimSpace(os.Getenv(profilingEnabledEnv)); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return settings, fmt.Errorf("parse %s: %w", profilingEnabledEnv, err)
		}
		settings.enabled = enabled
	}

	if raw := strings.TrimSpace(os.Getenv(profilingPortEnv)); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return settings, fmt.Errorf("parse %s: port must be between 1 and 65535", profilingPortEnv)
		}
		settings.port = port
	}

	return settings, nil
}

func newProfilingHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

func startProfilingServer() (*http.Server, error) {
	settings, err := profilingSettingsFromEnv()
	if err != nil {
		return nil, err
	}
	if !settings.enabled {
		return nil, nil
	}

	listener, err := net.Listen("tcp", settings.address())
	if err != nil {
		return nil, fmt.Errorf("listen for pprof on %s: %w", settings.address(), err)
	}

	server := &http.Server{
		Addr:              settings.address(),
		Handler:           newProfilingHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Printf("Profiling server stopped: %v", serveErr)
		}
	}()

	log.Printf("Profiling server started on http://%s/debug/pprof/", settings.address())
	return server, nil
}
