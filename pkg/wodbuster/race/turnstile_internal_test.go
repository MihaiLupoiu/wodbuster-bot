package race

import (
	"context"
	"errors"
	"testing"
	"time"
)

// take queues for a turn in the background and reports the priority once it
// gets one, holding the turn until release is called.
func take(t *testing.T, ts *turnstile, prio int, got chan<- int) {
	t.Helper()
	go func() {
		if err := ts.acquire(context.Background(), prio, prio, true); err != nil {
			t.Errorf("acquire %d: %v", prio, err)
			return
		}
		got <- prio
	}()
}

func next(t *testing.T, got <-chan int, within time.Duration) int {
	t.Helper()
	select {
	case p := <-got:
		return p
	case <-time.After(within):
		t.Fatalf("no turn granted within %v", within)
		return -1
	}
}

// Monday resolving a poll later than Friday must still book first.
func TestTurnstileWaitsForEveryGoalBeforeTheFirstTurn(t *testing.T) {
	ts := newTurnstile(3, time.Second)
	got := make(chan int, 3)

	take(t, ts, 4, got)
	take(t, ts, 2, got)
	time.Sleep(50 * time.Millisecond)
	select {
	case p := <-got:
		t.Fatalf("priority %d went before every goal arrived", p)
	default:
	}

	take(t, ts, 0, got)
	for _, want := range []int{0, 2, 4} {
		if p := next(t, got, time.Second); p != want {
			t.Fatalf("turn went to %d, want %d", p, want)
		}
		ts.release()
	}
}

// A goal stuck on a slow poll holds the others back only up to the cap.
func TestTurnstileOpensAfterTheCap(t *testing.T) {
	ts := newTurnstile(2, 80*time.Millisecond)
	got := make(chan int, 1)

	start := time.Now()
	take(t, ts, 3, got)
	next(t, got, time.Second)
	if waited := time.Since(start); waited < 70*time.Millisecond {
		t.Errorf("opened after %v, before the cap", waited)
	}
}

// Leaving the queue must not leak the turn: the next goal still gets it.
func TestTurnstileCancelledWaiterGivesWay(t *testing.T) {
	ts := newTurnstile(1, time.Second)
	if err := ts.acquire(context.Background(), 0, 0, true); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- ts.acquire(ctx, 1, 1, false) }()
	got := make(chan int, 1)
	go func() {
		if err := ts.acquire(context.Background(), 2, 2, false); err == nil {
			got <- 2
		}
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire: %v", err)
	}

	ts.release()
	if p := next(t, got, time.Second); p != 2 {
		t.Fatalf("turn went to %d, want 2", p)
	}
}
