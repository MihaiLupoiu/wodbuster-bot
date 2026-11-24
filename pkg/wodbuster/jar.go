package wodbuster

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"sync"
	"time"
)

// recordingJar is a cookie jar that remembers the full cookies it was given.
//
// cookiejar.Jar.Cookies returns names and values only, so once a cookie is in
// it the expiry is gone. WodBuster renews .WBAuth in the Set-Cookie of ordinary
// requests, moving its expiry forward, and a caller that stores sessions needs
// that renewed cookie, expiry included. This wrapper hands every call to the
// real jar and keeps its own copy of the latest version of each cookie.
//
// Cookies are keyed by name and path, the same key browserauth uses when it
// builds a Session. Domain is dropped, as it is there: NewClient sets every
// cookie on both the box host and wodbuster.com.
var _ http.CookieJar = (*recordingJar)(nil)

type recordingJar struct {
	jar *cookiejar.Jar

	mu      sync.Mutex
	order   []string
	cookies map[string]*http.Cookie
}

func newRecordingJar() (*recordingJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &recordingJar{jar: jar, cookies: map[string]*http.Cookie{}}, nil
}

func (r *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	// cookiejar.Jar panics on a nil cookie, and a stored Session that went
	// through JSON can hold one.
	if slices.Contains(cookies, nil) {
		cookies = slices.DeleteFunc(slices.Clone(cookies), func(c *http.Cookie) bool { return c == nil })
	}
	r.jar.SetCookies(u, cookies)
	r.record(cookies, time.Now())
}

func (r *recordingJar) Cookies(u *url.URL) []*http.Cookie { return r.jar.Cookies(u) }

func (r *recordingJar) record(cookies []*http.Cookie, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range cookies {
		if c == nil || c.Name == "" {
			continue
		}
		key := c.Name + "\x00" + c.Path
		if c.MaxAge < 0 || (!c.Expires.IsZero() && !c.Expires.After(now)) {
			// The server deleted it.
			if _, ok := r.cookies[key]; ok {
				delete(r.cookies, key)
				if i := slices.Index(r.order, key); i >= 0 {
					r.order = slices.Delete(r.order, i, i+1)
				}
			}
			continue
		}
		cp := *c
		cp.Domain = ""
		cp.Raw = ""
		cp.Unparsed = nil
		if cp.MaxAge > 0 {
			// Max-Age wins over Expires; turn it into a time that survives
			// being stored and read back days later.
			cp.Expires = now.Add(time.Duration(cp.MaxAge) * time.Second).UTC()
			cp.MaxAge = 0
		}
		if _, ok := r.cookies[key]; !ok {
			r.order = append(r.order, key)
		}
		r.cookies[key] = &cp
	}
}

// snapshot returns copies, in the order the cookies were first seen.
func (r *recordingJar) snapshot() []*http.Cookie {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*http.Cookie, 0, len(r.order))
	for _, k := range r.order {
		cp := *r.cookies[k]
		out = append(out, &cp)
	}
	return out
}
