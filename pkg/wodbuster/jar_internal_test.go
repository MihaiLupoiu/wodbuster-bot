package wodbuster

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// A nil cookie or one without a name has nothing to key it by; recording it
// would put a hole in the session.
func TestRecordingJarSkipsNilAndNamelessCookies(t *testing.T) {
	jar, err := newRecordingJar()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://fake.wodbuster.com")
	jar.SetCookies(u, []*http.Cookie{nil, {Name: "", Value: "x", Path: "/"}, {Name: "ok", Value: "1", Path: "/"}})
	got := jar.snapshot()
	if len(got) != 1 || got[0].Name != "ok" {
		t.Errorf("snapshot = %+v, want only ok", got)
	}
}

func TestRecordingJarDeletesOnAPastExpiry(t *testing.T) {
	jar, err := newRecordingJar()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://fake.wodbuster.com")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "a", Value: "1", Path: "/"},
		{Name: "b", Value: "1", Path: "/"},
		{Name: "c", Value: "1", Path: "/"},
	})
	jar.SetCookies(u, []*http.Cookie{{Name: "b", Value: "", Path: "/", Expires: time.Now().Add(-time.Minute)}})
	got := jar.snapshot()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Errorf("snapshot = %+v, want a and c in order", got)
	}
}
