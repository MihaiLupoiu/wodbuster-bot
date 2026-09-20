// Package wodbustertest runs a fake WodBuster that speaks the real protocol.
//
// It exists because the real thing can only be tested once a week. A gym
// publishes its schedule at a fixed hour on a fixed day; if the booking fails
// there is no second attempt until the next one. Generated mocks do not help
// with that — a mock asserts what you believe the client does, whereas this
// serves the bytes the client actually parses, and can be told to publish late,
// run out of places or reject a booking.
//
//	srv := wodbustertest.New(t)
//	srv.PublishAfter(500 * time.Millisecond)
//	srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Capacity: 12})
//	client, _ := wodbuster.NewClient(srv.Session())
package wodbustertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// Class describes a class to serve. Zero values are filled in with sane
// defaults by AddClass.
type Class struct {
	ID       int64
	Name     string
	Date     wodbuster.Date
	Start    string // "07:00"
	Capacity int
	Booked   int
	State    string // WodBuster's own TipoEstado; defaults to "Inscribible"
}

// Request is one call the client made, recorded for assertions.
type Request struct {
	Handler string // "LoadClass", "Calendario_Inscribir", ...
	ClassID int64
	Ticks   int64
	Idu     string
	At      time.Time

	// UserAgent and Cookies are what the client actually put on the wire.
	// Identity travels in the cookies, so a test that cares which athlete a
	// booking was made for has to look here.
	UserAgent string
	Cookies   []*http.Cookie
}

// Server is a fake WodBuster. Safe for concurrent use: the client under test
// will hit it from several goroutines at once, which is the point.
type Server struct {
	mu        sync.Mutex
	classes   []Class
	publishAt time.Time
	requests  []Request
	nextID    int64

	// RejectBooking, when set, makes every booking fail with this message
	// instead of consuming a place. Use it to exercise the losing path.
	rejectBooking string

	// ExpireSession makes every handler answer with the login page.
	expireSession bool

	// ServerSkew shifts the Date header, to test clock synchronisation.
	serverSkew time.Duration

	srv *httptest.Server
}

// New starts a fake server that publishes immediately and shuts down with t.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{nextID: 40000}
	s.srv = httptest.NewServer(s)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *Server) URL() string { return s.srv.URL }

// Session is a session pointing at this fake server. The Box is bogus on
// purpose — Client takes its base URL from the session, and tests override it
// through ClientOptions.
func (s *Server) Session() wodbuster.Session {
	return wodbuster.Session{
		Box:       "fake",
		AthleteID: "beca0b8c769d41fdbe5e29612b1ea99b",
		Cookies:   []*http.Cookie{{Name: "wb-test", Value: "1", Path: "/"}},
		IssuedAt:  time.Now(),
	}
}

// Transport routes every request to this fake server regardless of host, so a
// Client built from Session() reaches it.
func (s *Server) Transport() http.RoundTripper {
	target, _ := url.Parse(s.srv.URL)
	return &rewriteTransport{target: target, base: http.DefaultTransport}
}

// HTTPClient is the convenience form of Transport.
func (s *Server) HTTPClient() *http.Client {
	return &http.Client{Transport: s.Transport(), Timeout: 5 * time.Second}
}

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.Host = rt.target.Host
	return rt.base.RoundTrip(clone)
}

// PublishAfter hides the schedule until d from now. Until then the handlers
// answer exactly as the real site does for an unpublished day: no classes, and
// a countdown.
func (s *Server) PublishAfter(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishAt = time.Now().Add(d)
}

// AddClass registers a class and returns its id.
func (s *Server) AddClass(c Class) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == 0 {
		s.nextID++
		c.ID = s.nextID
	}
	if c.Capacity == 0 {
		c.Capacity = 12
	}
	if c.State == "" {
		c.State = "Inscribible"
	}
	if c.Name == "" {
		c.Name = "Wod"
	}
	if c.Start == "" {
		c.Start = "07:00"
	}
	s.classes = append(s.classes, c)
	return c.ID
}

// RejectBookings makes every booking fail with msg. Empty msg restores normal
// behaviour.
func (s *Server) RejectBookings(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectBooking = msg
}

// ExpireSession makes every handler answer with the login page.
func (s *Server) ExpireSession(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireSession = v
}

// SetSkew makes the server's Date header differ from the local clock.
func (s *Server) SetSkew(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverSkew = d
}

// Requests returns a copy of everything the client asked for.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count is how many times a handler was called.
func (s *Server) Count(handler string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Handler == handler {
			n++
		}
	}
	return n
}

// BookedCount is how many places have been taken on a class.
func (s *Server) BookedCount(id int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.classes {
		if c.ID == id {
			return c.Booked
		}
	}
	return -1
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	skew := s.serverSkew
	expired := s.expireSession
	s.mu.Unlock()

	w.Header().Set("Date", time.Now().Add(skew).UTC().Format(http.TimeFormat))

	q := r.URL.Query()
	id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	ticks, _ := strconv.ParseInt(q.Get("ticks"), 10, 64)
	handler := strings.TrimSuffix(pathBase(r.URL.Path), ".ashx")

	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Handler: handler, ClassID: id, Ticks: ticks, Idu: q.Get("idu"), At: time.Now(),
		UserAgent: r.Header.Get("User-Agent"), Cookies: r.Cookies(),
	})
	s.mu.Unlock()

	if expired {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `<!doctype html><html><head><title>Iniciar sesión</title></head>`+
			`<body><form action="/account/login.aspx"><input type="password"></form></body></html>`)
		return
	}

	switch handler {
	case "LoadClass":
		s.serveDay(w, ticks)
	case "Calendario_Inscribir":
		s.serveBook(w, id)
	case "Calendario_Avisar":
		writeResult(w, true, "")
	case "Calendario_Borrar":
		s.serveCancel(w, id)
	default:
		// reservas.aspx and anything else: an empty 200 with a Date header,
		// which is all ServerTime needs.
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>ok</body></html>")
	}
}

func (s *Server) serveDay(w http.ResponseWriter, ticks int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.publishAt.IsZero() && time.Now().Before(s.publishAt) {
		writeJSON(w, map[string]any{
			"Title":                    "sin publicar",
			"TipoNoClases":             "NoCalendar",
			"SegundosHastaPublicacion": time.Until(s.publishAt).Seconds(),
			"Data":                     []any{},
		})
		return
	}

	byHour := map[string][]any{}
	var order []string
	for _, c := range s.classes {
		if !c.Date.IsZero() && c.Date.String() != dateFromTicks(ticks) {
			continue
		}
		hour := c.Start + ":00"
		if _, seen := byHour[hour]; !seen {
			order = append(order, hour)
		}
		athletes := make([]map[string]any, c.Booked)
		for i := range athletes {
			athletes[i] = map[string]any{}
		}
		byHour[hour] = append(byHour[hour], map[string]any{
			"TipoEstado": c.State,
			"Valor": map[string]any{
				"Id": c.ID, "Nombre": c.Name, "Plazas": c.Capacity,
				"HoraComienzo": hour, "AtletasEntrenando": athletes,
			},
		})
	}

	data := make([]any, 0, len(order))
	for _, h := range order {
		data = append(data, map[string]any{"Hora": h, "Valores": byHour[h]})
	}
	writeJSON(w, map[string]any{
		"Title": "día de prueba", "SegundosHastaPublicacion": -100.0, "Data": data,
	})
}

func (s *Server) serveBook(w http.ResponseWriter, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rejectBooking != "" {
		writeResult(w, false, s.rejectBooking)
		return
	}
	for i := range s.classes {
		c := &s.classes[i]
		if c.ID != id {
			continue
		}
		if c.Booked >= c.Capacity {
			writeResult(w, false, "La clase está completa")
			return
		}
		c.Booked++
		c.State = "Borrable"
		writeResult(w, true, "")
		return
	}
	writeResult(w, false, "Clase inexistente")
}

func (s *Server) serveCancel(w http.ResponseWriter, id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.classes {
		c := &s.classes[i]
		if c.ID == id && c.Booked > 0 {
			c.Booked--
			c.State = "Inscribible"
			writeResult(w, true, "")
			return
		}
	}
	writeResult(w, false, "No estabas inscrito")
}

// writeResult answers an action the way WodBuster does: the verdict under
// "Res", wrapped in a body that also carries the refreshed day. Getting this
// shape wrong here is not a detail — the client was written against a guess of
// it, passed every test, and reported real bookings as failures in production.
func writeResult(w http.ResponseWriter, ok bool, msg string) {
	writeJSON(w, map[string]any{
		"Res": map[string]any{
			"EsCorrecto": ok, "ErrorMsg": msg, "NeedAdminConfirm": false,
		},
		"Mantenimiento": false,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func dateFromTicks(ticks int64) string {
	return time.Unix(ticks, 0).UTC().Format("2006-01-02")
}
