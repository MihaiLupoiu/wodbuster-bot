package usecase

import (
	"context"
	"sync"
	"time"
)

// runBlocker is a background job the weekly run must not overlap: the
// preflight and the session refresh both log athletes in through the same
// login gate as the run, and write the sessions the run reads. The run stops
// whichever is going before it starts.
//
// It holds one job at a time. A second one is refused rather than tracked
// alongside, so the run never has a job it cannot see to stop.
type runBlocker struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// begin registers a job that has just started, with the cancel that stops it.
// ok is false when another job of the same kind is still going: the caller
// must not start. Otherwise the job calls end when it no longer needs to hold
// the run back; end may be called more than once.
//
// A job registers before it checks whether the run is going. Then either the
// job sees the run's flag, or the run sees the job and stops it: there is no
// moment where neither does.
func (j *runBlocker) begin(cancel context.CancelFunc) (end func(), ok bool) {
	done := make(chan struct{})
	j.mu.Lock()
	if j.done != nil {
		j.mu.Unlock()
		return func() {}, false
	}
	j.cancel, j.done = cancel, done
	j.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			j.mu.Lock()
			j.cancel, j.done = nil, nil
			j.mu.Unlock()
			close(done)
		})
	}, true
}

// stop cancels the job, if one is going, and waits up to wait for it to end.
// It reports false when the job did not end in time; the caller goes ahead
// regardless, since nothing a job does is worth a missed booking.
func (j *runBlocker) stop(wait time.Duration) (running, ended bool) {
	j.mu.Lock()
	cancel, done := j.cancel, j.done
	j.mu.Unlock()
	if cancel == nil {
		return false, true
	}
	cancel()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
		return true, true
	case <-t.C:
		return true, false
	}
}

// stopAll stops several jobs at once and waits for each, up to wait in all.
func stopAll(wait time.Duration, jobs ...*runBlocker) []struct{ running, ended bool } {
	out := make([]struct{ running, ended bool }, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Go(func() {
			out[i].running, out[i].ended = j.stop(wait)
		})
	}
	wg.Wait()
	return out
}
