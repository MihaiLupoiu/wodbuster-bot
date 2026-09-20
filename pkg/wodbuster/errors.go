package wodbuster

import (
	"errors"
	"fmt"
	"strings"
)

// Callers branch on these with errors.Is. WodBuster's own Spanish messages must
// never leak into a caller's control flow: if a rejection does not match any of
// these, it arrives as *APIError with the original text preserved.
var (
	// ErrSessionExpired means the stored session is no longer accepted. The
	// caller decides whether to re-authenticate; this library never does it
	// on its own.
	ErrSessionExpired = errors.New("wodbuster: session expired")

	// ErrNotPublished means the box has not opened that day yet.
	ErrNotPublished = errors.New("wodbuster: schedule not published yet")

	// ErrClassNotFound means the day is published but has no such class.
	ErrClassNotFound = errors.New("wodbuster: class not found")

	// ErrClassFull means someone got there first.
	ErrClassFull = errors.New("wodbuster: class is full")

	// ErrAlreadyBooked means you were already in. Usually success, not failure.
	ErrAlreadyBooked = errors.New("wodbuster: already booked")

	// ErrNotIncluded means the athlete's plan does not cover this class.
	// Retrying will never help.
	ErrNotIncluded = errors.New("wodbuster: class not included in your plan")

	// ErrQuotaExceeded means the plan's allowance is spent (e.g. 3 sessions a
	// week). Like ErrNotIncluded, retrying is pointless — unlike ErrClassFull,
	// where it is the right move.
	ErrQuotaExceeded = errors.New("wodbuster: booking quota exceeded")
)

// APIError is a rejection from WodBuster that does not map to a sentinel above.
// Message is the server's own text, untranslated.
type APIError struct {
	Op      string // "Book", "Schedule", "JoinWaitlist"...
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("wodbuster: %s rejected with no reason given", e.Op)
	}
	return fmt.Sprintf("wodbuster: %s rejected: %s", e.Op, e.Message)
}

// classify turns a server rejection into one of the sentinels, or an *APIError.
//
// CAUTION: these substrings are a hypothesis. They were inferred from the
// site's minified JavaScript, not from observed failure responses — see the
// design doc's "things to verify". Anything unmatched falls through to
// *APIError with the text intact, so a wrong guess here degrades to a readable
// error rather than a silent misclassification. This function is the one place
// to fix when the real messages are known.
func classify(op, message string) error {
	m := strings.ToLower(message)
	switch {
	case containsAny(m, "completa", "llena", "sin plazas", "no hay plazas"):
		return fmt.Errorf("%s: %w (%s)", op, ErrClassFull, message)
	// "Ya estabas apuntado a esta clase." is what firespain answers to a second
	// Inscribir; the rest are variants seen or plausible on the same message.
	case containsAny(m, "ya estás", "ya estas", "ya estabas",
		"ya inscrito", "ya apuntado", "apuntado a esta clase", "inscrito en esta clase"):
		return fmt.Errorf("%s: %w (%s)", op, ErrAlreadyBooked, message)
	// Quota before plan: the real quota message mentions "tarifa" too — "ya te
	// has apuntado a todas las clases de tu tarifa y no tienes créditos
	// suficientes" — and matching "tarifa" first called an exhausted quota a
	// class outside the plan. They call for opposite reactions: one is "stop
	// for this week", the other is "this class was never yours to book".
	case containsAny(m, "límite", "limite", "máximo", "maximo", "has agotado",
		"sesiones", "todas las clases de tu tarifa", "créditos", "creditos"):
		return fmt.Errorf("%s: %w (%s)", op, ErrQuotaExceeded, message)
	case containsAny(m, "no incluye", "no incluida", "no incluido", "no está incluida"):
		return fmt.Errorf("%s: %w (%s)", op, ErrNotIncluded, message)
	case containsAny(m, "sesión", "sesion", "inicia sesión", "no autenticado"):
		return fmt.Errorf("%s: %w (%s)", op, ErrSessionExpired, message)
	default:
		return &APIError{Op: op, Message: message}
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
