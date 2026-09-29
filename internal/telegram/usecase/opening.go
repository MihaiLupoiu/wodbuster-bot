package usecase

import (
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
)

// Opening is when the box publishes the coming week.
//
// Sunday 12:00 Europe/Madrid for firespain, confirmed by live runs on
// 2026-09-20 and 2026-09-27. The bot previously assumed Saturday 11:55, a time
// at which nothing publishes — a failure that looks exactly like a bug in the
// booking code, which is why it is one value in one place now.
type Opening struct {
	Weekday  time.Weekday
	Time     string // "12:00"
	Location *time.Location

	// StartBefore is how early the run begins: long enough to log in (a
	// browser, 5-15s) and sync the clock, short enough that the session is
	// still fresh when the race starts.
	StartBefore time.Duration
}

func DefaultOpening(loc *time.Location) Opening {
	if loc == nil {
		loc = time.UTC
	}
	return Opening{
		Weekday:     time.Sunday,
		Time:        "12:00",
		Location:    loc,
		StartBefore: 10 * time.Minute,
	}
}

// Next is the next opening strictly after now.
func (o Opening) Next(now time.Time) time.Time {
	at, err := booking.NextOpening(now, o.Weekday, o.Time, o.Location)
	if err != nil {
		return now
	}
	return at
}

// cronSpec fires StartBefore ahead of the opening, in the opening's own zone.
func (o Opening) cronSpec() string {
	at := o.Next(time.Now()).Add(-o.StartBefore)
	return timeToCron(at)
}

func timeToCron(t time.Time) string {
	return itoa(t.Minute()) + " " + itoa(t.Hour()) + " * * " + itoa(int(t.Weekday()))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
