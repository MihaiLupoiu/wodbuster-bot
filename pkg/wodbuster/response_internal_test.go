package wodbuster

import (
	"net/http"
	"net/url"
	"testing"
)

// verdict is what getJSON makes of a response, in the order it checks.
func verdict(resp *http.Response, body []byte) string {
	switch {
	case looksLikeChallenge(resp, body):
		return "challenge"
	case looksLikeLogin(resp, body):
		return "expired"
	default:
		return "other"
	}
}

// An expired session costs a browser login and an admin alert, so only
// responses that really say "log in again" may count as one. Every other 403
// leaves the session alone.
func TestResponseVerdicts(t *testing.T) {
	const loginPage = `<!doctype html><html><head><title>Iniciar sesión</title></head>` +
		`<body><form action="/account/login.aspx"><input type="password"></form></body></html>`

	for _, tc := range []struct {
		name   string
		status int
		header http.Header
		path   string
		body   string
		want   string
	}{
		{"cloudflare challenge header", 403, http.Header{"Cf-Mitigated": {"challenge"}}, "/LoadClass", "", "challenge"},
		{"challenge title only", 403, nil, "/LoadClass",
			`<!DOCTYPE html><html><head><title>Just a moment...</title></head></html>`, "challenge"},
		{"attention required title", 403, nil, "/LoadClass",
			`<html><head><title>Attention Required! | Cloudflare</title></head></html>`, "challenge"},
		{"bare 403", 403, nil, "/LoadClass", "", "other"},
		{"cloudflare access denied", 403, http.Header{"Server": {"cloudflare"}}, "/LoadClass",
			`<html><head><title>Access denied | wodbuster.com used Cloudflare</title></head>` +
				`<body>Error 1020</body></html>`, "other"},
		{"403 with the login page", 403, nil, "/LoadClass", loginPage, "expired"},
		{"401", 401, nil, "/LoadClass", "", "expired"},
		{"login page with 200", 200, nil, "/LoadClass", loginPage, "expired"},
		{"redirected to login", 200, nil, "/account/login.aspx", "", "expired"},
		{"json", 200, nil, "/LoadClass", `{"Data":[]}`, "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.header
			if h == nil {
				h = http.Header{}
			}
			resp := &http.Response{
				StatusCode: tc.status,
				Header:     h,
				Request:    &http.Request{URL: &url.URL{Path: tc.path}},
			}
			if got := verdict(resp, []byte(tc.body)); got != tc.want {
				t.Errorf("verdict = %q, want %q", got, tc.want)
			}
		})
	}
}
