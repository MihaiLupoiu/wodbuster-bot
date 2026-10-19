package browserauth

import (
	"testing"

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
			pageState{Body: "¿quieres recordar este dispositivo?"}, StuckDevicePrompt},
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
