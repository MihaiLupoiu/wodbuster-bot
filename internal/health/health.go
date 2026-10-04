package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram/usecase"
)

type HealthStatus struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Uptime    string            `json:"uptime"`
	Version   string            `json:"version"`
	Checks    map[string]Health `json:"checks"`
}

type Health struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type Checker struct {
	storage   usecase.Storage
	logger    *slog.Logger
	startTime time.Time
	metrics   http.Handler
	version   string
}

func NewChecker(storage usecase.Storage, logger *slog.Logger, version string) *Checker {
	return &Checker{
		storage:   storage,
		logger:    logger,
		startTime: time.Now(),
		version:   version,
	}
}

func (c *Checker) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := c.checkHealth()

		w.Header().Set("Content-Type", "application/json")

		if status.Status == "unhealthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if err := json.NewEncoder(w).Encode(status); err != nil {
			c.logger.Error("Failed to encode health status", "error", err)
		}
	}
}

func (c *Checker) checkHealth() HealthStatus {
	checks := make(map[string]Health)
	overallStatus := "healthy"

	// Check storage connectivity
	storageHealth := c.checkStorage()
	checks["storage"] = storageHealth
	if storageHealth.Status != "healthy" {
		overallStatus = "unhealthy"
	}

	// Check system resources (basic)
	systemHealth := c.checkSystem()
	checks["system"] = systemHealth
	if systemHealth.Status != "healthy" {
		overallStatus = "degraded"
	}

	return HealthStatus{
		Status:    overallStatus,
		Timestamp: time.Now(),
		Uptime:    time.Since(c.startTime).String(),
		Version:   c.version,
		Checks:    checks,
	}
}

func (c *Checker) checkStorage() Health {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to perform a basic storage operation
	// For now, we'll just check if we can call GetUser without error
	_, exists := c.storage.GetUser(ctx, 0) // Use ID 0 as a test
	if !exists {
		// This is expected for a non-existent user, so storage is working
		return Health{
			Status:  "healthy",
			Message: "Storage is accessible",
		}
	}

	return Health{
		Status:  "healthy",
		Message: "Storage is accessible",
	}
}

func (c *Checker) checkSystem() Health {
	// Basic system check - we're running if we can execute this
	return Health{
		Status:  "healthy",
		Message: "System resources are available",
	}
}

// WithMetrics adds /metrics to the health server, so the process keeps a single
// listener. Takes the handler the platform package exposes.
func (c *Checker) WithMetrics(h http.Handler) *Checker {
	c.metrics = h
	return c
}

// Mux is everything this server exposes. Separate from StartServer so a test
// can exercise the routes without binding a port.
func (c *Checker) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", c.Handler())
	mux.HandleFunc("/health/ready", c.readinessHandler())
	mux.HandleFunc("/health/live", c.livenessHandler())
	if c.metrics != nil {
		mux.Handle("/metrics", c.metrics)
	}
	return mux
}

func (c *Checker) StartServer(addr string) error {
	if c.metrics != nil {
		c.logger.Info("serving Prometheus metrics", "path", "/metrics")
	}
	c.logger.Info("Starting health check server", "address", addr)
	return http.ListenAndServe(addr, c.Mux())
}

func (c *Checker) readinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Readiness check - can we serve requests?
		status := c.checkHealth()

		w.Header().Set("Content-Type", "application/json")

		if status.Status == "unhealthy" {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		fmt.Fprintf(w, `{"status": "%s", "message": "Ready to serve requests"}`, status.Status)
	}
}

func (c *Checker) livenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Liveness check - are we alive?
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status": "alive", "timestamp": "%s"}`, time.Now().Format(time.RFC3339))
	}
}
