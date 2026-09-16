package wodbuster

import (
	"fmt"
	"strings"
	"time"
)

// ClassID identifies one class on one day.
//
// It is EPHEMERAL. WodBuster creates the rows when the box publishes a week, so
// the id does not exist before publication and is different next week. Storing
// a ClassID is a bug: it is only meaningful between the moment a Schedule is
// read and the moment the booking is sent.
//
// The identity that survives is Target.
type ClassID int64

// ClassState is what you are allowed to do with a class right now. It is
// per-athlete: the same class is Bookable for one and NotIncluded for another.
type ClassState int

const (
	// StateUnknown means WodBuster reported a state this library does not know.
	// The class is still listed, with RawState carrying the original string.
	StateUnknown      ClassState = iota
	StateBookable                // room available
	StateWaitlistable            // full; only the waiting list is open
	StateBooked                  // you are already in
	StateNotIncluded             // your plan does not cover this class
)

func (s ClassState) String() string {
	switch s {
	case StateBookable:
		return "bookable"
	case StateWaitlistable:
		return "waitlistable"
	case StateBooked:
		return "booked"
	case StateNotIncluded:
		return "not-included"
	default:
		return "unknown"
	}
}

// parseState maps WodBuster's TipoEstado. An unrecognised value must not break
// the rest of the day, so it degrades to StateUnknown.
func parseState(raw string) ClassState {
	switch strings.TrimSpace(raw) {
	case "Inscribible":
		return StateBookable
	case "Avisable":
		return StateWaitlistable
	case "Borrable":
		return StateBooked
	case "NoTarifa", "Excluido":
		return StateNotIncluded
	default:
		return StateUnknown
	}
}

// Class is one class on one day, as seen by the authenticated athlete.
type Class struct {
	ID       ClassID
	Name     string // as the box writes it: "Wod", "Open box", "GYMaquinas"
	Date     Date
	Start    TimeOfDay
	Capacity int
	Booked   int
	State    ClassState

	raw string // the original TipoEstado, for diagnostics
}

// Free is how many places are left. It can go negative if the box overbooks.
func (c Class) Free() int { return c.Capacity - c.Booked }

func (c Class) IsFull() bool { return c.Free() <= 0 }

// RawState is WodBuster's own state string. For logs and for when something
// smells wrong — never branch on it, branch on State.
func (c Class) RawState() string { return c.raw }

// Target is this class's persistent identity.
func (c Class) Target() Target {
	return Target{Date: c.Date, Start: c.Start, Name: c.Name}
}

func (c Class) String() string {
	return fmt.Sprintf("%s %s %s (id %d, %d/%d, %s)",
		c.Date, c.Start, c.Name, c.ID, c.Booked, c.Capacity, c.State)
}

// Target names a class the way a human does: a date, a start time and the class
// name. Unlike ClassID it is stable forever, it means the same thing next year,
// and it is the only class identity worth persisting.
type Target struct {
	Date  Date      `json:"date"`
	Start TimeOfDay `json:"start"`
	Name  string    `json:"name"`
}

func (t Target) String() string {
	return fmt.Sprintf("%s %s %s %s", t.Date.Weekday(), t.Date, t.Start, t.Name)
}

func (t Target) Valid() error {
	if t.Date.IsZero() {
		return fmt.Errorf("wodbuster: target has no date")
	}
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("wodbuster: target has no class name")
	}
	return nil
}

// Schedule is one day's classes, plus whether that day is published at all.
type Schedule struct {
	Date    Date
	Classes []Class

	// Published is false when the box has not opened this day yet. Classes is
	// then empty and no ClassID exists.
	Published bool

	// OpensIn is how long the SERVER says is left until publication, measured
	// from the moment the response was produced. Zero when the server does not
	// say (already published, or no calendar at all).
	//
	// Deliberately a duration and not an instant: turning it into a wall-clock
	// time with the local clock is exactly the mistake to avoid.
	OpensIn time.Duration
}

// Resolve finds the class matching t. Name matching ignores case and
// surrounding space. Returns ErrClassNotFound if the day has no such class,
// and ErrNotPublished if the day is not out yet.
func (s Schedule) Resolve(t Target) (Class, error) {
	if !s.Published {
		return Class{}, fmt.Errorf("resolve %s: %w", t, ErrNotPublished)
	}
	want := strings.ToLower(strings.TrimSpace(t.Name))
	for _, c := range s.Classes {
		if c.Start == t.Start && strings.ToLower(strings.TrimSpace(c.Name)) == want {
			return c, nil
		}
	}
	return Class{}, fmt.Errorf("resolve %s: %w", t, ErrClassNotFound)
}

// At returns every class starting at that time, whatever its name.
func (s Schedule) At(t TimeOfDay) []Class {
	var out []Class
	for _, c := range s.Classes {
		if c.Start == t {
			out = append(out, c)
		}
	}
	return out
}
