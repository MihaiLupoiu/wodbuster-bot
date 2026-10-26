package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

var notFound = fmt.Errorf(`resolve Friday 2026-10-30 17:30 Open: %w; at 17:30 the box has "Open box"`,
	wodbuster.ErrClassNotFound)

type fakeCheck struct {
	mu       sync.Mutex
	busy     bool
	calls    int
	outcomes []booking.Outcome
	sent     []string
}

func (f *fakeCheck) checker(delay time.Duration) *nameChecker {
	return &nameChecker{
		delay: delay,
		busy: func() bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.busy
		},
		rehearse: func(context.Context, int64) ([]booking.Outcome, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls++
			return f.outcomes, nil
		},
		notify: func(_ int64, text string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.sent = append(f.sent, text)
		},
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		timers: make(map[int64]*time.Timer),
	}
}

func (f *fakeCheck) snapshot() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]string(nil), f.sent...)
}

// Five /book in a row cost one login, not five.
func TestNameCheckRunsOnceAfterABurst(t *testing.T) {
	f := &fakeCheck{outcomes: []booking.Outcome{{Class: "Friday 2026-10-30 17:30 Open", Status: "failed", Err: notFound}}}
	c := f.checker(50 * time.Millisecond)
	for range 5 {
		c.schedule(7)
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)

	calls, sent := f.snapshot()
	if calls != 1 {
		t.Errorf("rehearsed %d times, want 1", calls)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], `the box has "Open box"`) {
		t.Errorf("warning = %q", sent)
	}
}

// Logins at the opening belong to the run.
func TestNameCheckStaysOutOfABookingRun(t *testing.T) {
	f := &fakeCheck{busy: true}
	c := f.checker(10 * time.Millisecond)
	c.schedule(7)
	time.Sleep(60 * time.Millisecond)

	if calls, _ := f.snapshot(); calls != 0 {
		t.Errorf("rehearsed %d times during a run", calls)
	}
}

func TestNameCheckMessageOnlyReportsMissingClasses(t *testing.T) {
	quiet := []booking.Outcome{
		{Class: "Monday 2026-11-02 07:00 Wod", Status: "dry-run"},
		{Class: "Saturday 2026-10-31 09:00 Wod", Status: "failed", Err: errors.New("race: day never published")},
	}
	if msg := nameCheckMessage(quiet); msg != "" {
		t.Errorf("nothing to warn about, got %q", msg)
	}

	msg := nameCheckMessage(append(quiet, booking.Outcome{Class: "Friday 2026-10-30 17:30 Open", Status: "failed", Err: notFound}))
	if !strings.Contains(msg, "Friday 2026-10-30 17:30 Open") || strings.Contains(msg, "Monday") || strings.Contains(msg, "Saturday") {
		t.Errorf("message = %q", msg)
	}
	if !strings.Contains(msg, "/reset") {
		t.Errorf("message should say how to fix it: %q", msg)
	}
}
