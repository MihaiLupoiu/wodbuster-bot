// Package logging keeps secrets out of the logs, whoever writes them.
//
// The bot redacts what it knows is sensitive at the call site — a /login's
// password, for one. That does not cover text the bot does not write: the
// Telegram library puts the bot token in every request URL, and a network
// error quotes that URL verbatim, so on 2026-10-06 the token appeared in the
// container logs in full. Redacting at the handler catches it wherever it
// turns up.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// Redacted is what a secret is replaced with.
const Redacted = "<redacted>"

// minSecretLen keeps an empty or trivially short value from being treated as a
// secret, which would redact every occurrence of, say, "a".
const minSecretLen = 8

// Redactor replaces known secret values in text.
type Redactor struct {
	secrets []string
}

// NewRedactor redacts each of secrets. Values shorter than eight characters
// are ignored.
func NewRedactor(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, s := range secrets {
		if len(s) >= minSecretLen {
			r.secrets = append(r.secrets, s)
		}
	}
	return r
}

// String returns s with every secret replaced.
func (r *Redactor) String(s string) string {
	for _, secret := range r.secrets {
		if strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, Redacted)
		}
	}
	return s
}

// URIPassword is the password inside a connection string, or "" if it has
// none. It is how MONGO_URI's password gets onto the redaction list without
// redacting the whole URI.
func URIPassword(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.User == nil {
		return ""
	}
	p, _ := u.User.Password()
	return p
}

// Handler wraps another slog.Handler and redacts the message and every
// attribute before passing the record on.
type Handler struct {
	next slog.Handler
	r    *Redactor
}

// NewHandler redacts secrets from everything logged through next.
func NewHandler(next slog.Handler, r *Redactor) *Handler {
	return &Handler{next: next, r: r}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.String(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attr(a)
	}
	return &Handler{next: h.next.WithAttrs(clean), r: h.r}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), r: h.r}
}

func (h *Handler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.r.String(v.String()))
	case slog.KindGroup:
		group := v.Group()
		clean := make([]any, len(group))
		for i, g := range group {
			clean[i] = h.attr(g)
		}
		return slog.Group(a.Key, clean...)
	case slog.KindAny:
		// Errors and anything else that prints: a *url.Error carrying the
		// token is exactly this case. Only rewritten when it actually holds a
		// secret, so ordinary values keep their type in the output.
		s := fmt.Sprint(v.Any())
		if clean := h.r.String(s); clean != s {
			return slog.String(a.Key, clean)
		}
		return slog.Attr{Key: a.Key, Value: v}
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}

// PrintLogger adapts a slog.Logger to the Println/Printf shape older libraries
// log through — the Telegram one among them — so that their lines are JSON
// like everything else and pass through the same redaction.
type PrintLogger struct {
	Log   *slog.Logger
	Level slog.Level
	Msg   string
}

func (p PrintLogger) Println(v ...any) {
	p.Log.Log(context.Background(), p.Level, p.Msg,
		"detail", strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
}

func (p PrintLogger) Printf(format string, v ...any) {
	p.Log.Log(context.Background(), p.Level, p.Msg,
		"detail", strings.TrimSuffix(fmt.Sprintf(format, v...), "\n"))
}
