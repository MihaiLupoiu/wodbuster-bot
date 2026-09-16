package wodbuster

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Session is everything needed to act as one athlete: which box, who they are,
// and the cookies that prove it.
//
// It is a plain serializable value. This library does not persist it, does not
// know when it expires and never refreshes it — the consumer owns all of that.
// Treat it as a bearer credential: a stored Session is as sensitive as the
// password that minted it.
type Session struct {
	// Box is the centre's subdomain: "firespain" for firespain.wodbuster.com.
	Box string `json:"box"`

	// AthleteID is WodBuster's idu for this athlete.
	AthleteID string `json:"athlete_id"`

	// Cookies is the WHOLE jar, not just the one that looks important. A login
	// leaves several and picking one works right up until it doesn't.
	Cookies []*http.Cookie `json:"cookies"`

	IssuedAt time.Time `json:"issued_at"`
}

// Valid is a structural check only. It says nothing about whether the server
// still accepts the session — for that, call Client.Ping.
func (s Session) Valid() error {
	if strings.TrimSpace(s.Box) == "" {
		return fmt.Errorf("wodbuster: session has no box")
	}
	if strings.TrimSpace(s.AthleteID) == "" {
		return fmt.Errorf("wodbuster: session has no athlete id")
	}
	if len(s.Cookies) == 0 {
		return fmt.Errorf("wodbuster: session has no cookies")
	}
	return nil
}

// BaseURL is the box's origin.
func (s Session) BaseURL() string { return "https://" + s.Box + ".wodbuster.com" }

func (s Session) baseParsed() (*url.URL, error) {
	u, err := url.Parse(s.BaseURL())
	if err != nil {
		return nil, fmt.Errorf("wodbuster: bad box name %q: %w", s.Box, err)
	}
	return u, nil
}

// Redacted is a copy safe to log: cookie values are replaced by their length.
func (s Session) Redacted() Session {
	out := s
	out.Cookies = make([]*http.Cookie, 0, len(s.Cookies))
	for _, c := range s.Cookies {
		out.Cookies = append(out.Cookies, &http.Cookie{
			Name:  c.Name,
			Value: fmt.Sprintf("<%d bytes>", len(c.Value)),
			Path:  c.Path,
		})
	}
	return out
}
