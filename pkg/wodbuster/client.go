package wodbuster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// Client talks to one box as one athlete.
//
// Every method is exactly one HTTP request. No retries, no sleeps, no polling,
// no re-authentication: that is policy, and policy belongs to the caller (or to
// the race subpackage). A *Client is safe for concurrent use and cheap to
// build, so one per athlete is the intended shape.
type Client struct {
	http      *http.Client
	base      string
	session   Session
	userAgent string
	log       *slog.Logger
}

type config struct {
	httpClient *http.Client
	userAgent  string
	log        *slog.Logger
}

type Option func(*config)

// WithHTTPClient supplies the transport. Use it to set timeouts, a proxy, or —
// importantly for a scheduled run — an IdleConnTimeout longer than the wait,
// so the warm connection survives until the moment it is needed.
func WithHTTPClient(c *http.Client) Option { return func(cf *config) { cf.httpClient = c } }

func WithUserAgent(ua string) Option { return func(cf *config) { cf.userAgent = ua } }

func WithLogger(l *slog.Logger) Option { return func(cf *config) { cf.log = l } }

const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// NewClient builds a client for an authenticated session.
func NewClient(s Session, opts ...Option) (*Client, error) {
	if err := s.Valid(); err != nil {
		return nil, err
	}
	base, err := s.baseParsed()
	if err != nil {
		return nil, err
	}

	cf := config{userAgent: defaultUserAgent, log: slog.New(discardHandler{})}
	for _, o := range opts {
		o(&cf)
	}

	// The caller's http.Client is copied, never mutated, and the copy always
	// gets a jar of its own. Sharing one transport across athletes is the point
	// of WithHTTPClient — sharing a cookie jar would put two athletes' sessions
	// in the same request and book for whichever one the server saw last.
	var hc http.Client
	if cf.httpClient != nil {
		hc = *cf.httpClient
	} else {
		hc = http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     30 * time.Minute,
				ForceAttemptHTTP2:   true,
			},
		}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	hc.Jar = jar
	jar.SetCookies(base, s.Cookies)
	// The login happens on the parent domain; carry the cookies there too.
	if parent, err := url.Parse("https://wodbuster.com"); err == nil {
		jar.SetCookies(parent, s.Cookies)
	}

	return &Client{
		http:      &hc,
		base:      s.BaseURL(),
		session:   s,
		userAgent: cf.userAgent,
		log:       cf.log,
	}, nil
}

// Session returns the session this client was built from.
func (c *Client) Session() Session { return c.session }

// Box is the centre this client is bound to.
func (c *Client) Box() string { return c.session.Box }

func (c *Client) get(ctx context.Context, path string) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp, err
	}
	return body, resp, nil
}

// looksLikeLogin spots the usual shape of an expired session: the handler
// answers with the login page (or a redirect to it) instead of JSON.
func looksLikeLogin(resp *http.Response, body []byte) bool {
	if resp == nil {
		return false
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return true
	}
	if loc := resp.Header.Get("Location"); strings.Contains(strings.ToLower(loc), "login") {
		return true
	}
	if strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return true
	}
	head := strings.ToLower(string(body[:min(len(body), 2048)]))
	return strings.Contains(head, "<html") && strings.Contains(head, "login")
}

func (c *Client) getJSON(ctx context.Context, op, path string, out any) error {
	body, resp, err := c.get(ctx, path)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if looksLikeLogin(resp, body) {
		return fmt.Errorf("%s: %w", op, ErrSessionExpired)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected HTTP %d", op, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: unreadable response: %w", op, err)
	}
	return nil
}

// Schedule reads one day.
func (c *Client) Schedule(ctx context.Context, d Date) (Schedule, error) {
	var raw wireDay
	path := fmt.Sprintf("/athlete/handlers/LoadClass.ashx?ticks=%d&idu=%s",
		d.ticks(), url.QueryEscape(c.session.AthleteID))
	if err := c.getJSON(ctx, "Schedule", path, &raw); err != nil {
		return Schedule{}, err
	}
	return raw.toSchedule(d), nil
}

// Book takes the place. id must come from a Schedule read for the same date —
// see ClassID's doc on why it cannot be stored.
func (c *Client) Book(ctx context.Context, id ClassID, d Date) error {
	return c.action(ctx, "Book", "Calendario_Inscribir", id, d)
}

// JoinWaitlist signs up for the waiting list of a full class.
func (c *Client) JoinWaitlist(ctx context.Context, id ClassID, d Date) error {
	return c.action(ctx, "JoinWaitlist", "Calendario_Avisar", id, d)
}

// Cancel gives the place back.
func (c *Client) Cancel(ctx context.Context, id ClassID, d Date) error {
	return c.action(ctx, "Cancel", "Calendario_Borrar", id, d)
}

func (c *Client) action(ctx context.Context, op, handler string, id ClassID, d Date) error {
	var raw wireAction
	// connectionId is the SignalR channel for live updates; empty is fine.
	path := fmt.Sprintf("/athlete/handlers/%s.ashx?id=%d&ticks=%d&idu=%s&connectionId=",
		handler, id, d.ticks(), url.QueryEscape(c.session.AthleteID))
	if err := c.getJSON(ctx, op, path, &raw); err != nil {
		return err
	}
	if raw.EsCorrecto {
		return nil
	}
	msg := raw.message()
	c.log.Debug("wodbuster rejected an action", "op", op, "class", id, "message", msg)
	return classify(op, msg)
}

// Ping is the cheapest authenticated call there is. It answers one question —
// does the server still accept this session — and returns ErrSessionExpired
// when it does not.
//
// It doubles as the connection warmer: calling it shortly before a booking
// window reopens any TLS connection the transport let go idle.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Schedule(ctx, DateOf(time.Now()))
	return err
}

// ServerTime reads the server's own clock from the Date response header.
// Resolution is one second, truncated down — ServerClock corrects for that.
func (c *Client) ServerTime(ctx context.Context) (time.Time, error) {
	_, resp, err := c.get(ctx, "/athlete/reservas.aspx")
	if err != nil {
		return time.Time{}, fmt.Errorf("ServerTime: %w", err)
	}
	h := resp.Header.Get("Date")
	if h == "" {
		return time.Time{}, fmt.Errorf("ServerTime: server sent no Date header")
	}
	t, err := http.ParseTime(h)
	if err != nil {
		return time.Time{}, fmt.Errorf("ServerTime: unreadable Date header %q: %w", h, err)
	}
	return t, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }
