// Package race waits for a gym to publish its schedule and books the moment it
// does.
//
// It is built entirely on the wodbuster package's public API and has no access
// to anything private: if its policy does not suit you, ignore it and write
// your own loop. The primitives are deliberately dumb so that this can be.
package race

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// Goal is a class to chase plus what to do if it is already full.
type Goal struct {
	wodbuster.Target

	// Waitlist joins the waiting list when the class fills up first.
	Waitlist bool
}

// Outcome is how a goal ended.
type Outcome int

const (
	OutcomeFailed Outcome = iota
	OutcomeBooked
	OutcomeAlreadyBooked
	OutcomeWaitlisted
	OutcomeDryRun
)

func (o Outcome) String() string {
	switch o {
	case OutcomeBooked:
		return "booked"
	case OutcomeAlreadyBooked:
		return "already-booked"
	case OutcomeWaitlisted:
		return "waitlisted"
	case OutcomeDryRun:
		return "dry-run"
	default:
		return "failed"
	}
}

// Result is what happened to one goal.
type Result struct {
	Goal    Goal
	Class   wodbuster.Class // zero if the day never published
	Outcome Outcome
	Err     error
	At      time.Time // when it resolved
}

func (r Result) OK() bool {
	switch r.Outcome {
	case OutcomeBooked, OutcomeAlreadyBooked, OutcomeWaitlisted, OutcomeDryRun:
		return true
	}
	return false
}

// Options tunes the chase. The zero value is usable; Defaults fills it in.
type Options struct {
	Clock wodbuster.Clock

	// PollEvery is how often the schedule is asked for during the window.
	PollEvery time.Duration

	// StartBefore is how early polling begins, to absorb clock error.
	StartBefore time.Duration

	// GiveUpAfter is how long to keep trying past the opening.
	GiveUpAfter time.Duration

	// Attempts is how many times to retry a booking that lost the race.
	Attempts int

	// DryRun resolves everything and stops short of the booking call.
	DryRun bool

	Log *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.Clock == nil {
		o.Clock = wodbuster.SystemClock{}
	}
	if o.PollEvery <= 0 {
		o.PollEvery = 250 * time.Millisecond
	}
	if o.StartBefore <= 0 {
		o.StartBefore = 2 * time.Second
	}
	if o.GiveUpAfter <= 0 {
		o.GiveUpAfter = 90 * time.Second
	}
	if o.Attempts <= 0 {
		o.Attempts = 3
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	}
	return o
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// OpensAt asks the server when a day will be published, using its own countdown
// rather than a wall-clock guess. Returns ErrNoCountdown when the server does
// not say — either the day is already out, or there is no calendar for it.
func OpensAt(ctx context.Context, c *wodbuster.Client, d wodbuster.Date, clk wodbuster.Clock) (time.Time, error) {
	if clk == nil {
		clk = wodbuster.SystemClock{}
	}
	s, err := c.Schedule(ctx, d)
	if err != nil {
		return time.Time{}, err
	}
	if s.Published {
		return time.Time{}, fmt.Errorf("OpensAt %s: %w", d, ErrAlreadyPublished)
	}
	if s.OpensIn <= 0 {
		return time.Time{}, fmt.Errorf("OpensAt %s: %w", d, ErrNoCountdown)
	}
	return clk.Now().Add(s.OpensIn), nil
}

var (
	// ErrAlreadyPublished means the day is already out.
	ErrAlreadyPublished = errors.New("race: day already published")
	// ErrNoCountdown means the server gave no publication countdown.
	ErrNoCountdown = errors.New("race: server gave no countdown")
	// ErrNeverPublished means the deadline passed with the day still unpublished.
	ErrNeverPublished = errors.New("race: day never published")
)

// WaitUntil sleeps until opensAt, minus the head start, honouring the context.
func WaitUntil(ctx context.Context, opensAt time.Time, o Options) error {
	o = o.withDefaults()
	for {
		left := opensAt.Sub(o.Clock.Now()) - o.StartBefore
		if left <= 0 {
			return nil
		}
		if left > time.Second {
			left = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(left):
		}
	}
}

// Chase polls until the day publishes and then books, one goroutine per goal.
// It returns when every goal has resolved or the deadline passes; results come
// back in the same order as goals.
func Chase(ctx context.Context, c *wodbuster.Client, goals []Goal, o Options) []Result {
	o = o.withDefaults()
	deadline := time.Now().Add(o.StartBefore + o.GiveUpAfter)

	results := make([]Result, len(goals))
	var wg sync.WaitGroup
	for i, g := range goals {
		wg.Add(1)
		go func(i int, g Goal) {
			defer wg.Done()
			results[i] = chaseOne(ctx, c, g, o, deadline)
		}(i, g)
	}
	wg.Wait()
	return results
}

func chaseOne(ctx context.Context, c *wodbuster.Client, g Goal, o Options, deadline time.Time) Result {
	res := Result{Goal: g}

	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			res.Err, res.At = err, time.Now()
			return res
		}

		day, err := c.Schedule(ctx, g.Date)
		if err != nil {
			if errors.Is(err, wodbuster.ErrSessionExpired) {
				res.Err, res.At = err, time.Now()
				return res
			}
			o.Log.Debug("poll failed", "goal", g.Target.String(), "err", err)
			sleep(ctx, o.PollEvery)
			continue
		}
		if !day.Published {
			sleep(ctx, o.PollEvery)
			continue
		}

		class, err := day.Resolve(g.Target)
		if err != nil {
			res.Err, res.At = err, time.Now()
			return res
		}
		res.Class, res.At = class, time.Now()
		o.Log.Info("class resolved",
			"goal", g.Target.String(), "id", class.ID,
			"free", class.Free(), "capacity", class.Capacity, "state", class.State.String())

		switch class.State {
		case wodbuster.StateBooked:
			res.Outcome = OutcomeAlreadyBooked
			return res
		case wodbuster.StateNotIncluded:
			res.Outcome, res.Err = OutcomeFailed, wodbuster.ErrNotIncluded
			return res
		}

		if o.DryRun {
			res.Outcome = OutcomeDryRun
			return res
		}

		return book(ctx, c, g, class, o, res)
	}

	res.Outcome, res.Err, res.At = OutcomeFailed, ErrNeverPublished, time.Now()
	return res
}

func book(ctx context.Context, c *wodbuster.Client, g Goal, class wodbuster.Class, o Options, res Result) Result {
	var last error
	for attempt := 1; attempt <= o.Attempts; attempt++ {
		err := c.Book(ctx, class.ID, class.Date)
		if err == nil {
			res.Outcome, res.At = OutcomeBooked, time.Now()
			return res
		}
		last = err

		// Some failures will never become successes, however fast you retry.
		if errors.Is(err, wodbuster.ErrAlreadyBooked) {
			res.Outcome, res.At = OutcomeAlreadyBooked, time.Now()
			return res
		}
		if errors.Is(err, wodbuster.ErrNotIncluded) ||
			errors.Is(err, wodbuster.ErrQuotaExceeded) ||
			errors.Is(err, wodbuster.ErrSessionExpired) {
			break
		}
		o.Log.Debug("booking attempt failed",
			"goal", g.Target.String(), "attempt", attempt, "err", err)
		sleep(ctx, 150*time.Millisecond)
	}

	if g.Waitlist && errors.Is(last, wodbuster.ErrClassFull) {
		if err := c.JoinWaitlist(ctx, class.ID, class.Date); err == nil {
			res.Outcome, res.At = OutcomeWaitlisted, time.Now()
			return res
		}
	}

	res.Outcome, res.Err, res.At = OutcomeFailed, last, time.Now()
	return res
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
