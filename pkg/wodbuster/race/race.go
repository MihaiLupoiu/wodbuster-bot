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

	// Priority orders the booking calls: lower goes first. WodBuster takes one
	// booking per athlete at a time and the first one at an opening can take
	// seconds, so the order decides which class gets the early slot. Equal
	// priorities book in the order the goals were given.
	Priority int
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

	// Attempts is how many booking calls were made. One means it went in on
	// the first ask; more means the places were being fought over.
	Attempts int
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

	// Attempts caps the booking calls made for one goal. Zero — the default —
	// means no cap: keep trying until the class is full, the deadline passes,
	// or the server says something retrying cannot fix. A place freed by
	// somebody else two seconds in is still a place.
	Attempts int

	// RetryEvery is the pause between booking attempts.
	RetryEvery time.Duration

	// StatusEvery is how often the class is re-read while retrying. It is what
	// makes "keep trying" safe: it reports how many places are left, notices
	// the class filling up so the waiting list can take over, and catches a
	// booking that landed even though its answer did not come back.
	StatusEvery time.Duration

	// ResolveWait caps how long the first goal to find its class waits for the
	// others before booking starts. Without the wait, whichever goal resolves
	// first books first and Priority means nothing. The goals poll together,
	// so they normally resolve within a few tens of milliseconds and the wait
	// ends then; the cap only bites when one poll is slow or fails.
	ResolveWait time.Duration

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
	if o.Attempts < 0 {
		o.Attempts = 0
	}
	if o.RetryEvery <= 0 {
		o.RetryEvery = 150 * time.Millisecond
	}
	if o.StatusEvery <= 0 {
		o.StatusEvery = time.Second
	}
	if o.ResolveWait <= 0 {
		o.ResolveWait = 250 * time.Millisecond
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
// The polling is parallel; the booking calls go one at a time in Priority
// order. It returns when every goal has resolved or the deadline passes;
// results come back in the same order as goals.
func Chase(ctx context.Context, c *wodbuster.Client, goals []Goal, o Options) []Result {
	o = o.withDefaults()
	deadline := time.Now().Add(o.StartBefore + o.GiveUpAfter)
	turns := newTurnstile(len(goals), o.ResolveWait)
	defer turns.stop()

	results := make([]Result, len(goals))
	var wg sync.WaitGroup
	for i, g := range goals {
		wg.Add(1)
		go func(i int, g Goal) {
			defer wg.Done()
			q := queued{turns: turns, prio: g.Priority, seq: i, arrived: new(sync.Once)}
			results[i] = chaseOne(ctx, c, g, o, deadline, q)
			q.arrive() // no-op if it already arrived on its first booking call
		}(i, g)
	}
	wg.Wait()
	return results
}

func chaseOne(ctx context.Context, c *wodbuster.Client, g Goal, o Options, deadline time.Time, q queued) Result {
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

		return book(ctx, c, g, class, o, res, deadline, q)
	}

	res.Outcome, res.Err, res.At = OutcomeFailed, ErrNeverPublished, time.Now()
	return res
}

func book(ctx context.Context, c *wodbuster.Client, g Goal, class wodbuster.Class,
	o Options, res Result, deadline time.Time, q queued) Result {

	started := time.Now()
	lastStatus := started
	var last error

	for attempt := 1; ; attempt++ {
		if o.Attempts > 0 && attempt > o.Attempts {
			o.Log.Info("giving up on the attempt limit",
				"goal", g.Target.String(), "attempts", o.Attempts, "err", last)
			break
		}
		if !time.Now().Before(deadline) {
			o.Log.Info("giving up on the deadline",
				"goal", g.Target.String(), "attempts", attempt-1,
				"after", time.Since(started).Round(time.Millisecond), "err", last)
			break
		}
		if err := ctx.Err(); err != nil {
			last = err
			break
		}

		res.Attempts = attempt
		err := q.do(ctx, func() error { return c.Book(ctx, class.ID, class.Date) })
		if err == nil {
			o.Log.Info("booked", "goal", g.Target.String(), "id", class.ID, "priority", g.Priority,
				"attempt", attempt, "after", time.Since(started).Round(time.Millisecond))
			res.Outcome, res.At = OutcomeBooked, time.Now()
			return res
		}
		last = err

		// Some answers will never become successes, however fast you retry.
		if errors.Is(err, wodbuster.ErrAlreadyBooked) {
			res.Outcome, res.At = OutcomeAlreadyBooked, time.Now()
			return res
		}
		if errors.Is(err, wodbuster.ErrNotIncluded) ||
			errors.Is(err, wodbuster.ErrQuotaExceeded) ||
			errors.Is(err, wodbuster.ErrSessionExpired) {
			o.Log.Info("not retrying", "goal", g.Target.String(),
				"attempt", attempt, "err", err)
			break
		}

		o.Log.Debug("booking attempt failed",
			"goal", g.Target.String(), "attempt", attempt,
			"after", time.Since(started).Round(time.Millisecond), "err", err)

		// Full is the one failure that changes the plan rather than the odds.
		if errors.Is(err, wodbuster.ErrClassFull) {
			if !g.Waitlist {
				o.Log.Info("class is full and the waiting list is off",
					"goal", g.Target.String(), "attempt", attempt)
				break
			}
			if r, done := joinWaitlist(ctx, c, g, class, o, res, q); done {
				return r
			}
		}

		if time.Since(lastStatus) >= o.StatusEvery {
			lastStatus = time.Now()
			if r, done := status(ctx, c, g, &class, o, res, attempt, started, q); done {
				return r
			}
		}

		sleep(ctx, o.RetryEvery)
	}

	// Out of attempts or out of time. If the last thing we know is that the
	// class is full, the waiting list is still worth one try.
	if g.Waitlist && errors.Is(last, wodbuster.ErrClassFull) {
		if r, done := joinWaitlist(ctx, c, g, class, o, res, q); done {
			return r
		}
	}

	res.Outcome, res.Err, res.At = OutcomeFailed, last, time.Now()
	return res
}

// joinWaitlist tries the waiting list once. A refusal is not fatal: places come
// back when people cancel, so the caller keeps chasing the booking itself.
func joinWaitlist(ctx context.Context, c *wodbuster.Client, g Goal, class wodbuster.Class,
	o Options, res Result, q queued) (Result, bool) {

	if err := q.do(ctx, func() error { return c.JoinWaitlist(ctx, class.ID, class.Date) }); err != nil {
		o.Log.Warn("could not join the waiting list",
			"goal", g.Target.String(), "id", class.ID, "err", err)
		return res, false
	}
	o.Log.Info("joined the waiting list", "goal", g.Target.String(), "id", class.ID)
	res.Outcome, res.At = OutcomeWaitlisted, time.Now()
	res.Class = class
	return res, true
}

// status re-reads the class mid-retry: it is both the progress report and the
// escape hatch. It refreshes the id in place, since a ClassID is only valid
// for the read it came from.
func status(ctx context.Context, c *wodbuster.Client, g Goal, class *wodbuster.Class,
	o Options, res Result, attempt int, started time.Time, q queued) (Result, bool) {

	day, err := c.Schedule(ctx, class.Date)
	if err != nil {
		o.Log.Debug("status read failed", "goal", g.Target.String(), "err", err)
		return res, false
	}
	cur, err := day.Resolve(g.Target)
	if err != nil {
		o.Log.Debug("status read could not find the class",
			"goal", g.Target.String(), "err", err)
		return res, false
	}
	*class = cur
	res.Class = cur

	o.Log.Info("still trying", "goal", g.Target.String(), "id", cur.ID,
		"attempt", attempt, "free", cur.Free(), "capacity", cur.Capacity,
		"state", cur.State.String(), "elapsed", time.Since(started).Round(time.Millisecond))

	// The booking may have landed even though its answer did not come back.
	if cur.State == wodbuster.StateBooked {
		o.Log.Info("the class reads as booked; taking it",
			"goal", g.Target.String(), "id", cur.ID)
		res.Outcome, res.At = OutcomeAlreadyBooked, time.Now()
		return res, true
	}
	// No places left: retrying Book is now pointless, the waiting list is not.
	if cur.Free() <= 0 && g.Waitlist {
		return joinWaitlist(ctx, c, g, cur, o, res, q)
	}
	return res, false
}

// turnstile hands out booking turns one at a time, lowest priority first.
//
// The client already lets only one booking per athlete through, but its queue
// has no order: whichever goroutine wins the channel goes first. The turnstile
// sits in front of it so the order is ours. It also stays shut until every goal
// has resolved — or wait has passed since the first did — so a goal that found
// its class a poll earlier cannot jump ahead of a more important one.
type turnstile struct {
	mu      sync.Mutex
	pending int  // goals that have not arrived yet
	open    bool // the resolve barrier has lifted
	busy    bool // a turn is in progress
	waiting []*waiter
	wait    time.Duration
	timer   *time.Timer
}

type waiter struct {
	prio, seq int
	ready     chan struct{}
	granted   bool
}

func newTurnstile(goals int, wait time.Duration) *turnstile {
	return &turnstile{pending: goals, open: goals == 0, wait: wait}
}

// arrive marks one goal as finished without needing a turn.
func (t *turnstile) arrive() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.arriveLocked()
}

// arriveLocked counts one goal past resolution. The last arrival opens the
// barrier; the first starts the cap.
func (t *turnstile) arriveLocked() {
	t.pending--
	if t.pending <= 0 {
		t.openLocked()
		return
	}
	if t.timer == nil && !t.open {
		t.timer = time.AfterFunc(t.wait, func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.openLocked()
		})
	}
}

func (t *turnstile) openLocked() {
	t.open = true
	if t.timer != nil {
		t.timer.Stop()
	}
	t.dispatchLocked()
}

func (t *turnstile) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
}

// acquire waits for a turn. arriving says this is the goal's first call, so it
// also counts at the barrier — in the same step as joining the queue, or the
// last goal to arrive would open the barrier and lose the turn to a lower
// priority already waiting.
func (t *turnstile) acquire(ctx context.Context, prio, seq int, arriving bool) error {
	w := &waiter{prio: prio, seq: seq, ready: make(chan struct{})}
	t.mu.Lock()
	t.waiting = append(t.waiting, w)
	if arriving {
		t.arriveLocked()
	}
	t.dispatchLocked()
	t.mu.Unlock()

	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		t.mu.Lock()
		defer t.mu.Unlock()
		if w.granted {
			// The turn came as we gave up: pass it on.
			t.busy = false
			t.dispatchLocked()
		} else {
			t.removeLocked(w)
		}
		return ctx.Err()
	}
}

func (t *turnstile) release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.busy = false
	t.dispatchLocked()
}

func (t *turnstile) dispatchLocked() {
	if t.busy || !t.open || len(t.waiting) == 0 {
		return
	}
	next := t.waiting[0]
	for _, w := range t.waiting[1:] {
		if w.prio < next.prio || (w.prio == next.prio && w.seq < next.seq) {
			next = w
		}
	}
	t.removeLocked(next)
	t.busy = true
	next.granted = true
	close(next.ready)
}

func (t *turnstile) removeLocked(w *waiter) {
	for i, x := range t.waiting {
		if x == w {
			t.waiting = append(t.waiting[:i], t.waiting[i+1:]...)
			return
		}
	}
}

// queued is one goal's place at the turnstile.
type queued struct {
	turns   *turnstile
	prio    int
	seq     int
	arrived *sync.Once
}

// arrive counts the goal at the barrier once, however many paths call it.
func (q queued) arrive() { q.arrived.Do(q.turns.arrive) }

// do runs one booking call in its turn. The retry pause in book happens after
// the turn is released, so a class that keeps being refused does not hold up
// the ones behind it.
func (q queued) do(ctx context.Context, call func() error) error {
	arriving := false
	q.arrived.Do(func() { arriving = true })
	if err := q.turns.acquire(ctx, q.prio, q.seq, arriving); err != nil {
		return err
	}
	defer q.turns.release()
	return call()
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
