package browserauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests drive a real Chrome against local copies of WodBuster's pages,
// because the parts that broke on 2026-11-15 run inside the browser: what
// language the page is asked for, and the JavaScript that finds the "remember
// this device" question. They are skipped in -short mode and when no Chrome is
// installed; set WODBUSTER_CHROME_PATH to point at one.

// browserFor starts the browser Authenticate would start, against srv.
func browserFor(t *testing.T) context.Context {
	t.Helper()
	if testing.Short() {
		t.Skip("drives a real browser")
	}
	a := New(WithChromePath(os.Getenv("WODBUSTER_CHROME_PATH")))
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), a.allocatorOptions()...)
	t.Cleanup(cancelAlloc)
	ctx, cancel := chromedp.NewContext(allocCtx)
	t.Cleanup(cancel)
	ctx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancelTimeout)
	if err := chromedp.Run(ctx); err != nil {
		t.Skipf("no browser to drive: %v", err)
	}
	return ctx
}

// The page language follows Accept-Language since November 2026, so what the
// browser sends decides which page every text match is run against.
//
// It only proves something where Chrome would otherwise ask for English: on
// Linux, which takes the language from LANG — the container and CI. Chrome on
// macOS asks in the system's language, which may be Spanish already.
func TestBrowserAsksForSpanish(t *testing.T) {
	var mu sync.Mutex
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Get("Accept-Language")
		mu.Unlock()
		fmt.Fprint(w, "<html><body>ok</body></html>")
	}))
	defer srv.Close()

	ctx := browserFor(t)
	require.NoError(t, chromedp.Run(ctx, chromedp.Navigate(srv.URL)))

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, strings.HasPrefix(got, "es-ES"), "Accept-Language was %q", got)
}

func TestDevicePromptIsFoundInAnyLanguage(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer srv.Close()

	for _, tc := range []struct {
		page    string
		lang    string
		pickVia string
	}{
		{"device-prompt-es.html", "es", "id"},
		{"device-prompt-en.html", "en", "id"}, // what every login met on 2026-11-15
		{"device-prompt-en-new-ids.html", "en", "value"},
	} {
		t.Run(tc.page, func(t *testing.T) {
			ctx := browserFor(t)
			var page pageState
			require.NoError(t, chromedp.Run(ctx,
				chromedp.Navigate(srv.URL+"/"+tc.page),
				chromedp.Evaluate(jsPageState, &page)))
			assert.True(t, page.Device, "the device question must be recognised")
			assert.Equal(t, tc.lang, page.Lang)
			assert.Equal(t, StuckDevicePrompt,
				stuckReason("https://wodbuster.com/account/login.aspx", page))

			var via, picked string
			require.NoError(t, chromedp.Run(ctx,
				chromedp.Evaluate(fmt.Sprintf(jsPickDevice, idJustOnce, valJustOnce, deviceTextsJustOnce), &via),
				chromedp.Evaluate(`document.body.getAttribute('data-picked') || ''`, &picked)))
			assert.Equal(t, tc.pickVia, via)
			assert.Equal(t, valJustOnce, picked, "answers \"do not remember\"")
		})
	}
}

// The plain login form is not the device question, in either language, or
// the loop would click at radios that are not there.
func TestLoginFormIsNotTheDevicePrompt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html lang="en"><head><title>Sign in - WodBuster</title></head><body>
<form><input id="body_body_CtlLogin_IoEmail" type="email"><input id="body_body_CtlLogin_IoPassword" type="password">
<input type="submit" id="body_body_CtlLogin_CtlAceptar" value="Sign in"></form></body></html>`)
	}))
	defer srv.Close()

	ctx := browserFor(t)
	var page pageState
	require.NoError(t, chromedp.Run(ctx, chromedp.Navigate(srv.URL), chromedp.Evaluate(jsPageState, &page)))
	assert.False(t, page.Device)
	assert.Equal(t, StuckLoginPage, stuckReason("https://wodbuster.com/account/login.aspx", page))
}
