package wodbuster

import (
	"fmt"
	"strings"
	"time"
)

// Date is a calendar date with no time and no zone.
//
// It exists because WodBuster identifies a day with a "ticks" parameter that is
// the unix time of that calendar date's UTC midnight. That is not "the date as
// unix" and it is not an instant, and computing it from a time.Time by hand is
// the easiest mistake to make against this API. The conversion is unexported so
// callers cannot get it wrong.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// NewDate builds a Date from its parts.
func NewDate(year int, month time.Month, day int) Date {
	return Date{Year: year, Month: month, Day: day}
}

// DateOf takes the calendar date of t as seen in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// NextWeekday returns the next date falling on wd, strictly after from's date.
// If from is already that weekday, it jumps a full week.
func NextWeekday(from time.Time, wd time.Weekday) Date {
	d := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	delta := (int(wd) - int(d.Weekday()) + 7) % 7
	if delta == 0 {
		delta = 7
	}
	return DateOf(d.AddDate(0, 0, delta))
}

func (d Date) IsZero() bool { return d == Date{} }

func (d Date) Weekday() time.Weekday { return d.utc().Weekday() }

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// At returns the instant of this date at t in loc.
func (d Date) At(t TimeOfDay, loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, t.Hour, t.Minute, 0, 0, loc)
}

// Midnight returns this date's midnight in loc.
func (d Date) Midnight(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

func (d Date) utc() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

// ticks is the wire encoding: unix seconds of this calendar date's UTC midnight.
// Unexported on purpose — see the type doc.
func (d Date) ticks() int64 { return d.utc().Unix() }

// ParseDate reads "2006-01-02".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return Date{}, fmt.Errorf("wodbuster: invalid date %q, want YYYY-MM-DD", s)
	}
	return DateOf(t), nil
}

func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// TimeOfDay is a wall-clock time of day, to the minute.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// ParseTimeOfDay accepts "7:00", "07:00" and "07:00:00". Seconds, if given,
// must be zero: WodBuster has no classes starting at a non-zero second.
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	fail := func() (TimeOfDay, error) {
		return TimeOfDay{}, fmt.Errorf("wodbuster: invalid time of day %q, want HH:MM", s)
	}
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return fail()
	}
	var h, m, sec int
	if _, err := fmt.Sscanf(parts[0], "%d", &h); err != nil {
		return fail()
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &m); err != nil {
		return fail()
	}
	if len(parts) == 3 {
		if _, err := fmt.Sscanf(parts[2], "%d", &sec); err != nil || sec != 0 {
			return fail()
		}
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return fail()
	}
	return TimeOfDay{Hour: h, Minute: m}, nil
}

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute) }

func (t TimeOfDay) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

func (t *TimeOfDay) UnmarshalText(b []byte) error {
	parsed, err := ParseTimeOfDay(string(b))
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
