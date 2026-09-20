package wodbuster

import (
	"encoding/json"
	"errors"
	"testing"
)

// The bodies below are trimmed copies of what firespain answered on
// 2026-09-20. They exist because the first version of wireAction was deduced
// from minified JavaScript rather than observed: it looked for the verdict at
// the top level, found nothing, and turned two successful bookings into
// "rejected with no reason given". A test that pins the real shape is the
// cheapest way to keep that from coming back.
const (
	realRejection = `{"Mantenimiento":false,"Title":"Mañana, lunes 21/09","ToDay":1789948800,
		"Res":{"EsCorrecto":false,"ErrorMsg":"Ya estabas apuntado a esta clase.","NeedAdminConfirm":false},
		"Data":[]}`
	realSuccess = `{"Mantenimiento":false,"ToDay":1789948800,
		"Res":{"EsCorrecto":true,"ErrorMsg":"","NeedAdminConfirm":false},"Data":[]}`
	topLevelSuccess = `{"EsCorrecto":true,"ErrorMsg":""}`
)

func TestWireActionReadsTheVerdictUnderRes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		wantOK  bool
		wantMsg string
	}{
		{"rejection", realRejection, false, "Ya estabas apuntado a esta clase."},
		{"success", realSuccess, true, ""},
		{"top-level fallback", topLevelSuccess, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw wireAction
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			res := raw.result()
			if res.EsCorrecto != tc.wantOK {
				t.Errorf("EsCorrecto = %v, want %v", res.EsCorrecto, tc.wantOK)
			}
			if got := res.message(); got != tc.wantMsg {
				t.Errorf("message = %q, want %q", got, tc.wantMsg)
			}
		})
	}
}

// The wording matters: "Ya estabas apuntado" is what a second Inscribir gets,
// and the race package treats it as a booking that already landed.
func TestClassifyAlreadyBooked(t *testing.T) {
	err := classify("Book", "Ya estabas apuntado a esta clase.")
	if !errors.Is(err, ErrAlreadyBooked) {
		t.Errorf("classify(...) = %v, want ErrAlreadyBooked", err)
	}
}

func TestClassifyDoesNotConfuseCancelWithBooked(t *testing.T) {
	err := classify("Cancel", "No estabas inscrito")
	if errors.Is(err, ErrAlreadyBooked) {
		t.Errorf("classify(...) = %v, want a plain APIError", err)
	}
}

// Both messages are verbatim from firespain on 2026-09-20.
func TestClassifyRealMessages(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want error
	}{
		{"No puedes apuntarte porque ya te has apuntado a todas las clases de tu tarifa  y no tienes créditos suficientes de clases sueltas.", ErrQuotaExceeded},
		{"Ya estabas apuntado a esta clase.", ErrAlreadyBooked},
		{"La clase está completa", ErrClassFull},
	} {
		if err := classify("Book", tc.msg); !errors.Is(err, tc.want) {
			t.Errorf("classify(%q) = %v, want %v", tc.msg, err, tc.want)
		}
	}
}
