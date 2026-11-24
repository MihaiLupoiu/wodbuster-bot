package wodbuster_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/wodbustertest"
)

func cookieNamed(s wodbuster.Session, name string) *http.Cookie {
	for _, c := range s.Cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// WodBuster renews .WBAuth in the Set-Cookie of an ordinary schedule read.
// A stored session is only as good as the last renewal saved, so the client
// must hand it back.
func TestSessionReturnsRenewedCookies(t *testing.T) {
	srv := wodbustertest.New(t)
	sess := srv.Session()
	sess.Cookies = append(sess.Cookies, &http.Cookie{
		Name: wodbuster.AuthCookieName, Value: "old", Path: "/",
		Expires: time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	})
	c, err := wodbuster.NewClient(sess, wodbuster.WithHTTPClient(srv.HTTPClient()))
	if err != nil {
		t.Fatal(err)
	}

	renewedUntil := time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second)
	srv.RenewCookies(&http.Cookie{
		Name: wodbuster.AuthCookieName, Value: "new", Path: "/",
		Expires: renewedUntil, Secure: true, HttpOnly: true,
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	got := c.Session()
	auth := cookieNamed(got, wodbuster.AuthCookieName)
	if auth == nil || auth.Value != "new" {
		t.Fatalf(".WBAuth = %+v, want the renewed value", auth)
	}
	if !got.AuthExpiry().Equal(renewedUntil) {
		t.Errorf("AuthExpiry = %v, want %v", got.AuthExpiry(), renewedUntil)
	}
	if cookieNamed(got, "wb-test") == nil {
		t.Error("the cookie the server did not touch was lost")
	}
	if len(got.Cookies) != 2 {
		t.Errorf("got %d cookies, want 2 (renewal replaces, never duplicates)", len(got.Cookies))
	}
	if !got.IssuedAt.Equal(sess.IssuedAt) {
		t.Error("IssuedAt must stay the time of the login")
	}
	// The session the caller passed in is not modified behind its back.
	if cookieNamed(sess, wodbuster.AuthCookieName).Value != "old" {
		t.Error("NewClient's argument was mutated")
	}

	// The renewed session works on a fresh client, which is how a stored one
	// is used days later.
	c2, err := wodbuster.NewClient(got, wodbuster.WithHTTPClient(srv.HTTPClient()))
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	last := reqs[len(reqs)-1]
	var sent string
	for _, ck := range last.Cookies {
		if ck.Name == wodbuster.AuthCookieName {
			sent = ck.Value
		}
	}
	if sent != "new" {
		t.Errorf("a client built from the renewed session sent .WBAuth=%q, want new", sent)
	}
}

func TestSessionWithoutRenewalIsUnchanged(t *testing.T) {
	srv := wodbustertest.New(t)
	c := newClient(t, srv)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := c.Session()
	if len(got.Cookies) != 1 || got.Cookies[0].Name != "wb-test" || got.Cookies[0].Value != "1" {
		t.Errorf("cookies = %+v, want the original one", got.Cookies)
	}
}

func TestServerDeletedCookieLeavesTheSession(t *testing.T) {
	srv := wodbustertest.New(t)
	c := newClient(t, srv)
	srv.RenewCookies(&http.Cookie{Name: "wb-test", Value: "", Path: "/", MaxAge: -1})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ck := cookieNamed(c.Session(), "wb-test"); ck != nil {
		t.Errorf("deleted cookie still in the session: %+v", ck)
	}
}

func TestMaxAgeBecomesAnExpiry(t *testing.T) {
	srv := wodbustertest.New(t)
	c := newClient(t, srv)
	srv.RenewCookies(&http.Cookie{Name: wodbuster.AuthCookieName, Value: "x", Path: "/", MaxAge: 3600})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	exp := c.Session().AuthExpiry()
	if d := time.Until(exp); d < 59*time.Minute || d > time.Hour+time.Second {
		t.Errorf("AuthExpiry is %v away, want about an hour", d)
	}
}

// A stored session goes through JSON. The expiry must come back.
func TestAuthExpirySurvivesJSON(t *testing.T) {
	exp := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	s := wodbustertest.New(t).Session()
	s.Cookies = append(s.Cookies, &http.Cookie{Name: wodbuster.AuthCookieName, Value: "v", Path: "/", Expires: exp})
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back wodbuster.Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.AuthExpiry().Equal(exp) {
		t.Errorf("AuthExpiry after JSON = %v, want %v", back.AuthExpiry(), exp)
	}
}

func TestAuthExpiryZeroForSessionCookieOrMissing(t *testing.T) {
	s := wodbustertest.New(t).Session()
	if !s.AuthExpiry().IsZero() {
		t.Error("no .WBAuth: want zero")
	}
	s.Cookies = append(s.Cookies, &http.Cookie{Name: wodbuster.AuthCookieName, Value: "v"})
	if !s.AuthExpiry().IsZero() {
		t.Error("session cookie: want zero")
	}
}

// The race pings in parallel. Recording renewals must not race.
func TestConcurrentPingsWithRenewals(t *testing.T) {
	srv := wodbustertest.New(t)
	c := newClient(t, srv)
	srv.RenewCookies(&http.Cookie{Name: wodbuster.AuthCookieName, Value: "v", Path: "/",
		Expires: time.Now().Add(time.Hour)})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); _ = c.Ping(context.Background()) }()
		go func() { defer wg.Done(); _ = c.Session() }()
	}
	wg.Wait()
	if cookieNamed(c.Session(), wodbuster.AuthCookieName) == nil {
		t.Error("renewal not recorded")
	}
}

// A Cloudflare challenge says nothing about the session. Calling it expired
// would make the caller throw away a good session.
func TestChallengeIsNotAnExpiredSession(t *testing.T) {
	srv := wodbustertest.New(t)
	c := newClient(t, srv)
	srv.Challenge(true)
	err := c.Ping(context.Background())
	if !errors.Is(err, wodbuster.ErrChallenge) {
		t.Fatalf("err = %v, want ErrChallenge", err)
	}
	if errors.Is(err, wodbuster.ErrSessionExpired) {
		t.Fatal("a challenge must not read as an expired session")
	}

	srv.Challenge(false)
	srv.ExpireSession(true)
	if err := c.Ping(context.Background()); !errors.Is(err, wodbuster.ErrSessionExpired) {
		t.Fatalf("login page: err = %v, want ErrSessionExpired", err)
	}
}

func TestServerExpiredCookieLeavesTheSession(t *testing.T) {
	srv := wodbustertest.New(t)
	c := newClient(t, srv)
	srv.RenewCookies(&http.Cookie{Name: "wb-test", Value: "gone", Path: "/",
		Expires: time.Now().Add(-time.Hour)})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ck := cookieNamed(c.Session(), "wb-test"); ck != nil {
		t.Errorf("cookie expired by the server still in the session: %+v", ck)
	}
}

// A login without "remember this device" leaves .WBAuth a browser-session
// cookie. If the server renews a remembered one that way, the session stops
// being one that lasts, and AuthExpiry must say so instead of keeping the old
// date.
func TestServerDowngradingAuthToASessionCookie(t *testing.T) {
	srv := wodbustertest.New(t)
	sess := srv.Session()
	sess.Cookies = append(sess.Cookies, &http.Cookie{
		Name: wodbuster.AuthCookieName, Value: "old", Path: "/",
		Expires: time.Now().Add(45 * 24 * time.Hour).UTC().Truncate(time.Second),
	})
	c, err := wodbuster.NewClient(sess, wodbuster.WithHTTPClient(srv.HTTPClient()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Session().AuthExpiry().IsZero() {
		t.Fatal("seeded .WBAuth expiry lost before any request")
	}
	srv.RenewCookies(&http.Cookie{Name: wodbuster.AuthCookieName, Value: "new", Path: "/"})
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := c.Session()
	if auth := cookieNamed(got, wodbuster.AuthCookieName); auth == nil || auth.Value != "new" {
		t.Fatalf(".WBAuth = %+v, want the downgraded value", auth)
	}
	if exp := got.AuthExpiry(); !exp.IsZero() {
		t.Errorf("AuthExpiry = %v, want zero for a session cookie", exp)
	}
}

// A stored session whose .WBAuth has already expired must not hand that
// cookie back as if it were still good.
func TestSeedingAnExpiredAuthCookieDropsIt(t *testing.T) {
	srv := wodbustertest.New(t)
	sess := srv.Session()
	sess.Cookies = append(sess.Cookies, &http.Cookie{
		Name: wodbuster.AuthCookieName, Value: "stale", Path: "/",
		Expires: time.Now().Add(-time.Hour),
	})
	c, err := wodbuster.NewClient(sess, wodbuster.WithHTTPClient(srv.HTTPClient()))
	if err != nil {
		t.Fatal(err)
	}
	got := c.Session()
	if ck := cookieNamed(got, wodbuster.AuthCookieName); ck != nil {
		t.Errorf("expired .WBAuth kept: %+v", ck)
	}
	if cookieNamed(got, "wb-test") == nil || len(got.Cookies) != 1 {
		t.Errorf("cookies = %+v, want only wb-test", got.Cookies)
	}
}
