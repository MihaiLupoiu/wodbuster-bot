package race_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/race"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/wodbustertest"
)

var (
	monday = wodbuster.NewDate(2026, time.August, 31)
	at7    = wodbuster.TimeOfDay{Hour: 7}
)

func client(t *testing.T, srv *wodbustertest.Server) *wodbuster.Client {
	t.Helper()
	c, err := wodbuster.NewClient(srv.Session(), wodbuster.WithHTTPClient(srv.HTTPClient()))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func goal(waitlist bool) race.Goal {
	return race.Goal{
		Target:   wodbuster.Target{Date: monday, Start: at7, Name: "Wod"},
		Waitlist: waitlist,
	}
}

func fastOptions() race.Options {
	return race.Options{
		PollEvery:   20 * time.Millisecond,
		StartBefore: 50 * time.Millisecond,
		GiveUpAfter: 5 * time.Second,
		Attempts:    3,
	}
}

// The whole point: keep asking while the day is unpublished, then book the
// instant it appears.
func TestChaseWaitsForPublicationThenBooks(t *testing.T) {
	srv := wodbustertest.New(t)
	id := srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday, Capacity: 12, Booked: 11})
	srv.PublishAfter(400 * time.Millisecond)

	start := time.Now()
	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, fastOptions())

	if len(results) != 1 {
		t.Fatalf("got %d results", len(results))
	}
	r := results[0]
	if r.Outcome != race.OutcomeBooked {
		t.Fatalf("outcome = %v, err = %v", r.Outcome, r.Err)
	}
	if r.Class.ID != wodbuster.ClassID(id) {
		t.Errorf("resolved the wrong class: %d", r.Class.ID)
	}
	if srv.Count("LoadClass") < 5 {
		t.Errorf("expected repeated polling, got %d calls", srv.Count("LoadClass"))
	}
	if srv.Count("Calendario_Inscribir") != 1 {
		t.Errorf("expected exactly one booking call, got %d", srv.Count("Calendario_Inscribir"))
	}
	// It must react within about one poll interval of publication.
	if took := time.Since(start); took > 700*time.Millisecond {
		t.Errorf("reacted too slowly: %v", took)
	}
}

// Losing the race: retry, then fall back to the waiting list.
func TestChaseFallsBackToWaitlist(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday, Capacity: 12, Booked: 12})
	srv.RejectBookings("La clase está completa")

	o := fastOptions()
	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, o)

	if results[0].Outcome != race.OutcomeWaitlisted {
		t.Fatalf("outcome = %v, err = %v", results[0].Outcome, results[0].Err)
	}
	if got := srv.Count("Calendario_Inscribir"); got != o.Attempts {
		t.Errorf("attempts = %d, want %d", got, o.Attempts)
	}
	if srv.Count("Calendario_Avisar") != 1 {
		t.Errorf("waitlist calls = %d, want 1", srv.Count("Calendario_Avisar"))
	}
}

// Without the waitlist option it must fail rather than silently sign up.
func TestChaseWithoutWaitlistFails(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday, Capacity: 12, Booked: 12})
	srv.RejectBookings("La clase está completa")

	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(false)}, fastOptions())

	if results[0].Outcome != race.OutcomeFailed {
		t.Fatalf("outcome = %v", results[0].Outcome)
	}
	if !errors.Is(results[0].Err, wodbuster.ErrClassFull) {
		t.Errorf("err = %v, want ErrClassFull", results[0].Err)
	}
	if srv.Count("Calendario_Avisar") != 0 {
		t.Error("joined the waiting list without being asked to")
	}
}

// A dry run must resolve everything and touch nothing.
func TestChaseDryRunNeverBooks(t *testing.T) {
	srv := wodbustertest.New(t)
	id := srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})

	o := fastOptions()
	o.DryRun = true
	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, o)

	if results[0].Outcome != race.OutcomeDryRun {
		t.Fatalf("outcome = %v", results[0].Outcome)
	}
	if results[0].Class.ID != wodbuster.ClassID(id) {
		t.Error("a dry run should still resolve the class id")
	}
	if srv.Count("Calendario_Inscribir") != 0 {
		t.Error("the dry run called the booking handler")
	}
}

// Already in: success, and no pointless booking call.
func TestChaseAlreadyBooked(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday, State: "Borrable", Booked: 5})

	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, fastOptions())

	if results[0].Outcome != race.OutcomeAlreadyBooked {
		t.Fatalf("outcome = %v, err = %v", results[0].Outcome, results[0].Err)
	}
	if srv.Count("Calendario_Inscribir") != 0 {
		t.Error("tried to book a class it was already in")
	}
}

// A plan that does not cover the class is not a retryable condition.
func TestChaseNotIncludedStopsImmediately(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday, State: "NoTarifa"})

	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, fastOptions())

	if results[0].Outcome != race.OutcomeFailed {
		t.Fatalf("outcome = %v", results[0].Outcome)
	}
	if !errors.Is(results[0].Err, wodbuster.ErrNotIncluded) {
		t.Errorf("err = %v", results[0].Err)
	}
	if srv.Count("Calendario_Inscribir") != 0 {
		t.Error("should not have tried to book")
	}
}

// Several goals go in parallel, not one after another.
func TestChaseRunsGoalsConcurrently(t *testing.T) {
	srv := wodbustertest.New(t)
	wed := wodbuster.NewDate(2026, time.September, 2)
	fri := wodbuster.NewDate(2026, time.September, 4)
	for _, d := range []wodbuster.Date{monday, wed, fri} {
		srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: d, Capacity: 12})
	}
	srv.PublishAfter(200 * time.Millisecond)

	goals := []race.Goal{
		{Target: wodbuster.Target{Date: monday, Start: at7, Name: "Wod"}},
		{Target: wodbuster.Target{Date: wed, Start: at7, Name: "Wod"}},
		{Target: wodbuster.Target{Date: fri, Start: at7, Name: "Wod"}},
	}

	start := time.Now()
	results := race.Chase(context.Background(), client(t, srv), goals, fastOptions())
	elapsed := time.Since(start)

	for i, r := range results {
		if r.Outcome != race.OutcomeBooked {
			t.Errorf("goal %d: outcome = %v, err = %v", i, r.Outcome, r.Err)
		}
	}
	// Sequentially this would be at least three publication waits.
	if elapsed > 500*time.Millisecond {
		t.Errorf("goals look sequential: %v", elapsed)
	}
}

// Results must line up with the goals that produced them.
func TestChaseKeepsResultOrder(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})
	srv.AddClass(wodbustertest.Class{Name: "Open box", Start: "08:00", Date: monday})

	goals := []race.Goal{
		{Target: wodbuster.Target{Date: monday, Start: wodbuster.TimeOfDay{Hour: 8}, Name: "Open box"}},
		{Target: wodbuster.Target{Date: monday, Start: at7, Name: "Wod"}},
	}
	results := race.Chase(context.Background(), client(t, srv), goals, fastOptions())

	if results[0].Goal.Name != "Open box" || results[1].Goal.Name != "Wod" {
		t.Errorf("results out of order: %q, %q", results[0].Goal.Name, results[1].Goal.Name)
	}
}

func TestChaseGivesUpWhenNothingPublishes(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})
	srv.PublishAfter(time.Hour)

	o := fastOptions()
	o.GiveUpAfter = 200 * time.Millisecond
	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, o)

	if !errors.Is(results[0].Err, race.ErrNeverPublished) {
		t.Errorf("err = %v, want ErrNeverPublished", results[0].Err)
	}
}

func TestChaseStopsOnCancel(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})
	srv.PublishAfter(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()

	done := make(chan []race.Result, 1)
	go func() { done <- race.Chase(ctx, client(t, srv), []race.Goal{goal(true)}, fastOptions()) }()

	select {
	case results := <-done:
		if results[0].Err == nil {
			t.Error("expected the cancellation to surface as an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Chase ignored the cancelled context")
	}
}

// An expired session must abort rather than burn the whole window retrying.
func TestChaseAbortsOnExpiredSession(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})
	srv.ExpireSession(true)

	o := fastOptions()
	o.GiveUpAfter = 5 * time.Second

	start := time.Now()
	results := race.Chase(context.Background(), client(t, srv), []race.Goal{goal(true)}, o)

	if !errors.Is(results[0].Err, wodbuster.ErrSessionExpired) {
		t.Fatalf("err = %v", results[0].Err)
	}
	if time.Since(start) > time.Second {
		t.Error("kept polling after the session was rejected")
	}
}

func TestOpensAtUsesTheServerCountdown(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})
	srv.PublishAfter(30 * time.Minute)

	at, err := race.OpensAt(context.Background(), client(t, srv), monday, wodbuster.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(at); d < 29*time.Minute || d > 30*time.Minute+time.Second {
		t.Errorf("OpensAt says %v from now, want about 30 minutes", d)
	}
}

func TestOpensAtOnPublishedDay(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Date: monday})

	_, err := race.OpensAt(context.Background(), client(t, srv), monday, nil)
	if !errors.Is(err, race.ErrAlreadyPublished) {
		t.Errorf("err = %v, want ErrAlreadyPublished", err)
	}
}

func TestWaitUntilRespectsTheHeadStart(t *testing.T) {
	o := fastOptions()
	o.StartBefore = 150 * time.Millisecond
	target := time.Now().Add(300 * time.Millisecond)

	start := time.Now()
	if err := race.WaitUntil(context.Background(), target, o); err != nil {
		t.Fatal(err)
	}
	waited := time.Since(start)
	if waited > 220*time.Millisecond {
		t.Errorf("waited %v; should have returned a head start early", waited)
	}
}
