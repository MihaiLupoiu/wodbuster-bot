package wodbuster_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/wodbustertest"
)

func newClient(t *testing.T, srv *wodbustertest.Server) *wodbuster.Client {
	t.Helper()
	c, err := wodbuster.NewClient(srv.Session(), wodbuster.WithHTTPClient(srv.HTTPClient()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// ticks is the one encoding that must match the real site exactly: unix seconds
// of the calendar date's UTC midnight. These values were read off the live
// service on 2026-08-23.
func TestDateTicksMatchTheRealService(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00"})
	c := newClient(t, srv)

	cases := []struct {
		date wodbuster.Date
		want int64
	}{
		{wodbuster.NewDate(2026, time.August, 24), 1787529600}, // Monday
		{wodbuster.NewDate(2026, time.August, 26), 1787702400}, // Wednesday
		{wodbuster.NewDate(2026, time.August, 28), 1787875200}, // Friday
	}
	for _, tc := range cases {
		if _, err := c.Schedule(context.Background(), tc.date); err != nil {
			t.Fatalf("Schedule(%s): %v", tc.date, err)
		}
		reqs := srv.Requests()
		got := reqs[len(reqs)-1].Ticks
		if got != tc.want {
			t.Errorf("ticks for %s = %d, want %d", tc.date, got, tc.want)
		}
	}
}

// A time.Time anywhere in the day must produce the same date, and the local
// zone must not shift it.
func TestDateOfIgnoresTimeAndZone(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Skip("no tzdata")
	}
	for _, tt := range []time.Time{
		time.Date(2026, time.August, 24, 0, 0, 0, 0, madrid),
		time.Date(2026, time.August, 24, 23, 59, 59, 0, madrid),
		time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC),
	} {
		if got := wodbuster.DateOf(tt); got.String() != "2026-08-24" {
			t.Errorf("DateOf(%s) = %s", tt, got)
		}
	}
}

func TestNextWeekday(t *testing.T) {
	sunday := time.Date(2026, time.August, 23, 12, 30, 0, 0, time.UTC)
	cases := map[time.Weekday]string{
		time.Monday:    "2026-08-24",
		time.Wednesday: "2026-08-26",
		time.Friday:    "2026-08-28",
		time.Sunday:    "2026-08-30", // today is Sunday: jump a week
	}
	for wd, want := range cases {
		if got := wodbuster.NextWeekday(sunday, wd).String(); got != want {
			t.Errorf("NextWeekday(%v) = %s, want %s", wd, got, want)
		}
	}
}

func TestParseTimeOfDay(t *testing.T) {
	ok := map[string]string{
		"7:00": "07:00", "07:00": "07:00", "07:00:00": "07:00", "19:30": "19:30",
	}
	for in, want := range ok {
		got, err := wodbuster.ParseTimeOfDay(in)
		if err != nil {
			t.Errorf("ParseTimeOfDay(%q): %v", in, err)
			continue
		}
		if got.String() != want {
			t.Errorf("ParseTimeOfDay(%q) = %s, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"", "7", "25:00", "07:61", "siete", "07:00:30"} {
		if _, err := wodbuster.ParseTimeOfDay(bad); err == nil {
			t.Errorf("ParseTimeOfDay(%q) should have failed", bad)
		}
	}
}

func TestScheduleResolve(t *testing.T) {
	srv := wodbustertest.New(t)
	wod := srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Capacity: 12, Booked: 11})
	srv.AddClass(wodbustertest.Class{Name: "GYMaquinas", Start: "07:00", Capacity: 8, State: "NoTarifa"})
	srv.AddClass(wodbustertest.Class{Name: "Open box", Start: "08:00", Capacity: 8, Booked: 2})

	day, err := newClient(t, srv).Schedule(context.Background(), wodbuster.NewDate(2026, time.August, 24))
	if err != nil {
		t.Fatal(err)
	}
	if !day.Published {
		t.Fatal("day should be published")
	}

	at7 := wodbuster.TimeOfDay{Hour: 7}
	class, err := day.Resolve(wodbuster.Target{Date: day.Date, Start: at7, Name: "  wOd "})
	if err != nil {
		t.Fatalf("Resolve should ignore case and spaces: %v", err)
	}
	if class.ID != wodbuster.ClassID(wod) {
		t.Errorf("resolved id = %d, want %d", class.ID, wod)
	}
	if class.Free() != 1 || class.IsFull() {
		t.Errorf("free = %d, full = %v", class.Free(), class.IsFull())
	}
	if class.State != wodbuster.StateBookable {
		t.Errorf("state = %v", class.State)
	}

	gym, _ := day.Resolve(wodbuster.Target{Date: day.Date, Start: at7, Name: "GYMaquinas"})
	if gym.State != wodbuster.StateNotIncluded {
		t.Errorf("NoTarifa should map to StateNotIncluded, got %v", gym.State)
	}
	if gym.RawState() != "NoTarifa" {
		t.Errorf("RawState should keep the original: %q", gym.RawState())
	}

	_, err = day.Resolve(wodbuster.Target{Date: day.Date, Start: wodbuster.TimeOfDay{Hour: 9}, Name: "Wod"})
	if !errors.Is(err, wodbuster.ErrClassNotFound) {
		t.Errorf("missing class should be ErrClassNotFound, got %v", err)
	}
}

// An unknown TipoEstado must not take the rest of the day down with it.
func TestUnknownStateDoesNotBreakTheDay(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", State: "AlgoNuevo"})
	srv.AddClass(wodbustertest.Class{Name: "Open box", Start: "08:00"})

	day, err := newClient(t, srv).Schedule(context.Background(), wodbuster.NewDate(2026, time.August, 24))
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Classes) != 2 {
		t.Fatalf("expected both classes, got %d", len(day.Classes))
	}
	if day.Classes[0].State != wodbuster.StateUnknown {
		t.Errorf("unknown state should be StateUnknown, got %v", day.Classes[0].State)
	}
	if day.Classes[0].RawState() != "AlgoNuevo" {
		t.Errorf("raw state lost: %q", day.Classes[0].RawState())
	}
	if day.Classes[1].State != wodbuster.StateBookable {
		t.Errorf("the healthy class should still parse, got %v", day.Classes[1].State)
	}
}

func TestUnpublishedDay(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00"})
	srv.PublishAfter(time.Hour)

	day, err := newClient(t, srv).Schedule(context.Background(), wodbuster.NewDate(2026, time.August, 31))
	if err != nil {
		t.Fatal(err)
	}
	if day.Published {
		t.Error("day should not be published")
	}
	if len(day.Classes) != 0 {
		t.Errorf("unpublished day should have no classes, got %d", len(day.Classes))
	}
	if day.OpensIn < 55*time.Minute || day.OpensIn > time.Hour {
		t.Errorf("OpensIn = %v, want about an hour", day.OpensIn)
	}
	_, err = day.Resolve(wodbuster.Target{Date: day.Date, Start: wodbuster.TimeOfDay{Hour: 7}, Name: "Wod"})
	if !errors.Is(err, wodbuster.ErrNotPublished) {
		t.Errorf("resolving an unpublished day should be ErrNotPublished, got %v", err)
	}
}

func TestBookConsumesAPlace(t *testing.T) {
	srv := wodbustertest.New(t)
	id := srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Capacity: 2})
	c := newClient(t, srv)
	date := wodbuster.NewDate(2026, time.August, 24)

	if err := c.Book(context.Background(), wodbuster.ClassID(id), date); err != nil {
		t.Fatalf("Book: %v", err)
	}
	if got := srv.BookedCount(id); got != 1 {
		t.Errorf("booked = %d, want 1", got)
	}

	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	if last.Handler != "Calendario_Inscribir" || last.ClassID != id || last.Idu == "" {
		t.Errorf("unexpected booking request: %+v", last)
	}
	if last.Ticks != 1787529600 {
		t.Errorf("booking sent ticks %d, want 1787529600", last.Ticks)
	}
}

func TestBookFullClassIsTyped(t *testing.T) {
	srv := wodbustertest.New(t)
	id := srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Capacity: 1, Booked: 1})

	err := newClient(t, srv).Book(context.Background(),
		wodbuster.ClassID(id), wodbuster.NewDate(2026, time.August, 24))
	if !errors.Is(err, wodbuster.ErrClassFull) {
		t.Fatalf("want ErrClassFull, got %v", err)
	}
}

// A rejection we do not recognise must survive as an APIError with the server's
// own words, not be silently swallowed or misfiled.
func TestUnknownRejectionBecomesAPIError(t *testing.T) {
	srv := wodbustertest.New(t)
	id := srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00"})
	srv.RejectBookings("Algo totalmente inesperado")

	err := newClient(t, srv).Book(context.Background(),
		wodbuster.ClassID(id), wodbuster.NewDate(2026, time.August, 24))

	var apiErr *wodbuster.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %T: %v", err, err)
	}
	if apiErr.Message != "Algo totalmente inesperado" {
		t.Errorf("original message lost: %q", apiErr.Message)
	}
	if apiErr.Op != "Book" {
		t.Errorf("op = %q", apiErr.Op)
	}
}

func TestExpiredSessionIsDetected(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00"})
	srv.ExpireSession(true)
	c := newClient(t, srv)

	_, err := c.Schedule(context.Background(), wodbuster.NewDate(2026, time.August, 24))
	if !errors.Is(err, wodbuster.ErrSessionExpired) {
		t.Errorf("Schedule: want ErrSessionExpired, got %v", err)
	}
	if err := c.Ping(context.Background()); !errors.Is(err, wodbuster.ErrSessionExpired) {
		t.Errorf("Ping: want ErrSessionExpired, got %v", err)
	}
}

func TestServerClockCorrectsDrift(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.SetSkew(42 * time.Second)

	clk, err := wodbuster.NewServerClock(context.Background(), newClient(t, srv), 3)
	if err != nil {
		t.Fatal(err)
	}
	drift := time.Until(clk.Now()) - 42*time.Second
	if drift > 1500*time.Millisecond || drift < -1500*time.Millisecond {
		t.Errorf("clock off by %v after correction", drift)
	}
}

func TestNewClientRejectsBadSessions(t *testing.T) {
	good := wodbustertest.New(t).Session()
	for name, mutate := range map[string]func(*wodbuster.Session){
		"no box":     func(s *wodbuster.Session) { s.Box = "" },
		"no athlete": func(s *wodbuster.Session) { s.AthleteID = "" },
		"no cookies": func(s *wodbuster.Session) { s.Cookies = nil },
	} {
		s := good
		mutate(&s)
		if _, err := wodbuster.NewClient(s); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRedactedHidesCookieValues(t *testing.T) {
	s := wodbustertest.New(t).Session()
	for _, c := range s.Redacted().Cookies {
		if c.Value == "1" {
			t.Error("Redacted still contains the cookie value")
		}
	}
}

// Two athletes, one transport. Sharing the transport is the whole point of
// WithHTTPClient — sharing a cookie jar would put both sessions in the same
// request, and the server would book for whichever it recognised last.
func TestClientsDoNotShareACookieJar(t *testing.T) {
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00"})
	shared := srv.HTTPClient()

	build := func(who string) *wodbuster.Client {
		t.Helper()
		s := srv.Session()
		s.AthleteID = who
		s.Cookies = []*http.Cookie{{Name: "wb-session", Value: who, Path: "/"}}
		c, err := wodbuster.NewClient(s, wodbuster.WithHTTPClient(shared))
		if err != nil {
			t.Fatalf("NewClient(%s): %v", who, err)
		}
		return c
	}

	alice, bob := build("alice"), build("bob")
	day := wodbuster.NewDate(2026, time.August, 24)
	if _, err := alice.Schedule(context.Background(), day); err != nil {
		t.Fatalf("alice: %v", err)
	}
	if _, err := bob.Schedule(context.Background(), day); err != nil {
		t.Fatalf("bob: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 2 {
		t.Fatalf("want 2 requests, got %d", len(reqs))
	}
	for i, want := range []string{"alice", "bob"} {
		if got := len(reqs[i].Cookies); got != 1 {
			t.Fatalf("%s carried %d cookies, want exactly its own", want, got)
		}
		if got := reqs[i].Cookies[0].Value; got != want {
			t.Errorf("%s's request carried the session of %q", want, got)
		}
	}
}

// The caller's http.Client must come back unchanged: it is theirs, it is very
// likely shared, and a jar installed behind their back would outlive this test.
func TestNewClientDoesNotMutateTheCallersHTTPClient(t *testing.T) {
	srv := wodbustertest.New(t)
	caller := srv.HTTPClient()
	if _, err := wodbuster.NewClient(srv.Session(), wodbuster.WithHTTPClient(caller)); err != nil {
		t.Fatal(err)
	}
	if caller.Jar != nil {
		t.Error("NewClient installed a cookie jar on the caller's http.Client")
	}
}

func TestUserAgent(t *testing.T) {
	const custom = "wodbook/1.2.3"
	srv := wodbustertest.New(t)
	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00"})
	day := wodbuster.NewDate(2026, time.August, 24)

	c, err := wodbuster.NewClient(srv.Session(),
		wodbuster.WithHTTPClient(srv.HTTPClient()), wodbuster.WithUserAgent(custom))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Schedule(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if got := srv.Requests()[0].UserAgent; got != custom {
		t.Errorf("User-Agent = %q, want %q", got, custom)
	}

	if _, err := newClient(t, srv).Schedule(context.Background(), day); err != nil {
		t.Fatal(err)
	}
	if got := srv.Requests()[1].UserAgent; got == "" || got == custom {
		t.Errorf("default User-Agent = %q, want the library's own", got)
	}
}
