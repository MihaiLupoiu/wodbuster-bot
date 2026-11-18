package browserauth

import (
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/stretchr/testify/assert"
)

// Where a stuck login stopped is the whole diagnosis: a Cloudflare challenge
// and WodBuster's own pages misbehaving need different fixes.
func TestStuckReason(t *testing.T) {
	for name, tc := range map[string]struct {
		href string
		page pageState
		want string
	}{
		"challenge wins over the page it sits on": {
			"https://wodbuster.com/account/login.aspx", pageState{CF: true}, StuckCloudflare},
		"device prompt": {
			"https://wodbuster.com/account/login.aspx",
			pageState{Body: "¿quieres recordar este dispositivo?", Device: true}, StuckDevicePrompt},
		"device prompt in English, as on 2026-11-15": {
			"https://wodbuster.com/account/login.aspx",
			pageState{Body: "sign in\nremember this device?", Lang: "en", Device: true}, StuckDevicePrompt},
		"roadtobox, as on 2026-10-14": {
			"https://wodbuster.com/account/roadtobox.aspx", pageState{}, StuckRoadToBox},
		"back on the login form, as on 2026-10-18": {
			"https://wodbuster.com/account/login.aspx", pageState{}, StuckLoginPage},
		"somewhere else": {"https://wodbuster.com/error.aspx", pageState{}, StuckOther},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, stuckReason(tc.href, tc.page))
		})
	}
}

func TestStuckErrorSaysWhereItStopped(t *testing.T) {
	err := &StuckError{Reason: StuckRoadToBox, URL: "https://wodbuster.com/account/roadtobox.aspx", Title: "WodBuster"}
	assert.Contains(t, err.Error(), "did not finish in time", "keeps the wording the metrics and alerts match on")
	assert.Contains(t, err.Error(), "roadtobox")
}

// A stored session needs to know when .WBAuth runs out, and Chrome is the
// only one that saw the Set-Cookie at login.
func TestToHTTPCookiesKeepsExpiry(t *testing.T) {
	exp := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	got := toHTTPCookies([]*network.Cookie{
		{Name: ".WBAuth", Value: "a", Domain: ".wodbuster.com", Path: "/", Expires: float64(exp.Unix())},
		{Name: "__Host-WBForms", Value: "b", Domain: "firespain.wodbuster.com", Path: "/", Expires: -1, Session: true},
		{Name: "other", Value: "c", Domain: "example.com", Path: "/"},
	}, "firespain")
	if assert.Len(t, got, 2) {
		assert.Equal(t, ".WBAuth", got[0].Name)
		assert.True(t, got[0].Expires.Equal(exp), "expiry %v", got[0].Expires)
		assert.Empty(t, got[0].Domain, "domain stays out, NewClient sets both hosts")
		assert.True(t, got[1].Expires.IsZero(), "session cookie has no expiry")
	}
}
