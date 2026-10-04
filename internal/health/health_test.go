package health_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/health"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/platform/metrics"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/storage"
	"github.com/MihaiLupoiu/wodbuster-bot/internal/telegram"
)

func checker(t *testing.T) *health.Checker {
	t.Helper()
	return health.NewChecker(storage.NewMemoryStorage(),
		slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
}

// The bot has one listener, so /metrics rides along with the health routes.
func TestMetricsAreServedNextToHealth(t *testing.T) {
	prom := metrics.NewPrometheus()
	telegram.NewMetrics(prom.Registry()).ObserveCommand("book")

	srv := httptest.NewServer(checker(t).WithMetrics(prom.Handler).Mux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), `wodbuster_bot_commands_total{command="book"} 1`)

	// The health routes still work.
	h, err := http.Get(srv.URL + "/health/live")
	require.NoError(t, err)
	defer h.Body.Close()
	assert.Equal(t, http.StatusOK, h.StatusCode)
}

// Without metrics the server is what it was: /metrics simply is not there.
func TestMetricsAreOptional(t *testing.T) {
	srv := httptest.NewServer(checker(t).Mux())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
