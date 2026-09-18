// Package browserauth logs in to WodBuster by driving a headless Chrome.
//
// Why a browser for this and plain HTTP for everything else: the login is an
// ASP.NET WebForms page with CSRFToken, __VIEWSTATE, __VIEWSTATEC and
// __EVENTVALIDATION, followed by a "remember this device" step that lives in an
// UpdatePanel. Replaying that by hand is possible but brittle in a way that
// breaks silently. Chrome just does it.
//
// It costs 5-15 seconds and a few hundred MB, so it belongs in a warm-up phase
// minutes before a booking window, never inside it. Once Authenticate returns a
// Session, the browser is gone and the race runs on plain HTTP.
//
// This package is separate so that the core has no browser dependency. Note
// that chromedp does NOT bundle a browser: Chrome or Chromium must be installed
// (`apt install chromium`), which is the practical cost of this approach.
package browserauth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

const loginURL = "https://wodbuster.com/account/login.aspx"

// headlessUserAgent replaces the one Chrome sends when it has no window, which
// says "HeadlessChrome" and which Cloudflare — sitting in front of WodBuster —
// answers with an interstitial the login form never arrives behind. Any
// ordinary Chrome UA gets through; it is the giveaway string that is the
// problem, not the automation.
const headlessUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

// Known control ids, as of 2026-08. They are tried first and the DOM is
// searched by shape if they have moved — see jsMarkFields.
const (
	idEmail       = "body_body_CtlLogin_IoEmail"
	idPassword    = "body_body_CtlLogin_IoPassword"
	idSubmit      = "body_body_CtlLogin_CtlAceptar"
	idTrustDevice = "body_body_CtlConfiar_CtlSeguro"
	idJustOnce    = "body_body_CtlConfiar_CtlNoSeguro"
)

// The "remember this device" choice is a pair of radios that post back on
// click. Their ids are WebForms-generated, but the value= is the control's own
// name and has survived the id changing under us once already.
const (
	valTrustDevice = "CtlSeguro"
	valJustOnce    = "CtlNoSeguro"
)

// Selectors used for typing: jsMarkFields tags the real elements with data-wb
// so we never depend on the literal ASP.NET ids.
const (
	selEmail    = `[data-wb="email"]`
	selPassword = `[data-wb="password"]`
	selSubmit   = `[data-wb="submit"]`
)

// reAthleteID pulls the idu out of the Crossfit.Init(...) call the booking page
// prints inline. It is the sixth argument.
var reAthleteID = regexp.MustCompile(`Crossfit\.Init\(\s*(?:[^,]*,\s*){5}'([^']+)'`)

// jsDescribeClickables lists every clickable on the page with its id and
// trimmed text. When the "remember this device" step changes, this is what
// tells us the new control names without a round trip to the browser.
const jsDescribeClickables = `(function(){
  var els = document.querySelectorAll('a,button,input[type=submit],input[type=button],[onclick],[role=button]');
  var out = [];
  for (var i = 0; i < els.length && out.length < 25; i++) {
    var t = ((els[i].innerText || els[i].textContent || els[i].value || '') + '')
      .replace(/\s+/g, ' ').trim().slice(0, 80);
    out.push({tag: els[i].tagName, id: els[i].id || '', text: t});
  }
  return out;
})()`

// jsPickDevice answers the "remember this device" question. It tries the known
// id, then the radio's value=, then the visible label — three independent ways
// in, because this step is the one that has already broken once and it breaks
// after the password is typed, where a failure costs a whole booking window.
// It reports which route worked so a drift shows up in the logs.
const jsPickDevice = `(function(id, value, texts){
  function fire(el, via) {
    if (!el) return '';
    el.click();
    return via;
  }
  var byID = document.getElementById(id);
  if (byID) return fire(byID, 'id');

  var radios = document.querySelectorAll('input[type=radio]');
  for (var i = 0; i < radios.length; i++) {
    if (radios[i].value === value) return fire(radios[i], 'value');
  }

  var labels = document.querySelectorAll('label[for]');
  for (var i = 0; i < labels.length; i++) {
    var t = ((labels[i].innerText || labels[i].textContent || '') + '').toLowerCase();
    for (var j = 0; j < texts.length; j++) {
      if (t.indexOf(texts[j]) !== -1) {
        return fire(document.getElementById(labels[i].htmlFor), 'label');
      }
    }
  }
  return '';
})(%q, %q, %s)`

// jsMarkFields locates the three login controls and tags them.
//
// WebForms ids are generated from control nesting, so moving a control in the
// designer renames it. Falling back to shape — the form that contains a
// password input — survives that. The return value says which route was taken
// for each field so the caller can warn when the ids have drifted.
const jsMarkFields = `(function(){
  var out = {};
  function mark(el, name, via) {
    if (!el) return false;
    el.setAttribute('data-wb', name);
    out[name] = via;
    return true;
  }
  mark(document.getElementById('body_body_CtlLogin_IoEmail'), 'email', 'id');
  mark(document.getElementById('body_body_CtlLogin_IoPassword'), 'password', 'id');
  mark(document.getElementById('body_body_CtlLogin_CtlAceptar'), 'submit', 'id');

  var pass = document.querySelector('input[type=password]');
  var form = pass ? pass.form : document.querySelector('form');
  if (form) {
    if (!out.password) mark(pass, 'password', 'shape');
    if (!out.email) {
      mark(form.querySelector('input[type=email]') || form.querySelector('input[type=text]'),
           'email', 'shape');
    }
    if (!out.submit) {
      var b = form.querySelectorAll('input[type=submit],button[type=submit],button:not([type])');
      for (var i = 0; i < b.length; i++) {
        var t = ((b[i].value || b[i].innerText || '') + '').toLowerCase();
        if (t.indexOf('acept') !== -1 || t.indexOf('entrar') !== -1 || t.indexOf('iniciar') !== -1) {
          mark(b[i], 'submit', 'text');
          break;
        }
      }
      if (!out.submit && b.length) mark(b[0], 'submit', 'first');
    }
  }
  return out;
})()`

// jsClickByText clicks the first clickable element whose text contains one of
// the given lowercase strings.
const jsClickByText = `(function(texts){
  var els = document.querySelectorAll('a,button,input[type=submit],input[type=button],[onclick],[role=button]');
  for (var i = 0; i < els.length; i++) {
    var t = ((els[i].innerText || els[i].textContent || els[i].value || '') + '').toLowerCase();
    if (!t) continue;
    for (var j = 0; j < texts.length; j++) {
      if (t.indexOf(texts[j]) !== -1) { els[i].click(); return texts[j]; }
    }
  }
  return '';
})(%s)`

// Credentials are an athlete's WodBuster login.
//
// They are used once, to mint a Session, and are not retained. Callers should
// decrypt them as late as possible and drop them straight after.
type Credentials struct {
	Email    string
	Password string
}

// Authenticator drives a browser to produce a Session. It satisfies the shape
// the consumer is expected to depend on, so a future HTTP-only implementation
// can replace it without touching callers.
type Authenticator struct {
	chromePath     string
	headless       bool
	trustDevice    bool
	diagnosticsDir string
	userAgent      string
	timeout        time.Duration
	log            *slog.Logger
}

type Option func(*Authenticator)

// WithChromePath points at a specific browser binary. By default chromedp
// searches the usual locations (chromium, chromium-browser, google-chrome...).
func WithChromePath(p string) Option { return func(a *Authenticator) { a.chromePath = p } }

// WithHeadless(false) opens a real window. Useful when the login breaks and you
// want to watch what the page is doing.
func WithHeadless(v bool) Option { return func(a *Authenticator) { a.headless = v } }

// WithTrustDevice picks "remember this device" instead of "just once". Only
// worth setting if the resulting cookie is going to be persisted.
func WithTrustDevice(v bool) Option { return func(a *Authenticator) { a.trustDevice = v } }

// WithDiagnosticsDir makes a failed login leave an HTML dump and a screenshot
// in that directory. Passwords are stripped from the HTML.
func WithDiagnosticsDir(d string) Option { return func(a *Authenticator) { a.diagnosticsDir = d } }

// WithUserAgent overrides the User-Agent used in headless mode. The default
// already passes Cloudflare; set this only if that stops being true.
func WithUserAgent(ua string) Option {
	return func(a *Authenticator) {
		if ua != "" {
			a.userAgent = ua
		}
	}
}

func WithTimeout(d time.Duration) Option { return func(a *Authenticator) { a.timeout = d } }

func WithLogger(l *slog.Logger) Option { return func(a *Authenticator) { a.log = l } }

func New(opts ...Option) *Authenticator {
	a := &Authenticator{
		headless:  true,
		userAgent: headlessUserAgent,
		timeout:   90 * time.Second,
		log:       slog.New(slog.NewTextHandler(io_discard{}, &slog.HandlerOptions{Level: slog.LevelError + 1})),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

type io_discard struct{}

func (io_discard) Write(p []byte) (int, error) { return len(p), nil }

// Authenticate logs in and returns a Session. The browser is closed before it
// returns.
func (a *Authenticator) Authenticate(ctx context.Context, box string, cr Credentials) (wodbuster.Session, error) {
	var zero wodbuster.Session
	if strings.TrimSpace(box) == "" {
		return zero, fmt.Errorf("browserauth: no box given")
	}
	if cr.Email == "" || cr.Password == "" {
		return zero, fmt.Errorf("browserauth: email and password are required")
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", a.headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1280, 900),
	)
	if a.headless {
		opts = append(opts, chromedp.UserAgent(a.userAgent))
	}
	if a.chromePath != "" {
		opts = append(opts, chromedp.ExecPath(a.chromePath))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx,
		chromedp.WithErrorf(func(f string, v ...any) { a.log.Debug(fmt.Sprintf(f, v...)) }))
	defer cancelBrowser()

	runCtx, cancelRun := context.WithTimeout(browserCtx, a.timeout)
	defer cancelRun()

	a.log.Info("authenticating", "box", box, "email", cr.Email)

	if err := chromedp.Run(runCtx,
		chromedp.Navigate(loginURL),
		chromedp.WaitReady("form", chromedp.ByQuery),
	); err != nil {
		return zero, fmt.Errorf("browserauth: could not open the login page: %w", err)
	}

	// A cookie banner, if present, gets the most private answer available. If
	// there is none, nothing happens and we carry on.
	var dismissed string
	_ = chromedp.Run(runCtx, chromedp.Evaluate(
		fmt.Sprintf(jsClickByText, `["rechazar","solo las necesarias","solo necesarias","denegar"]`),
		&dismissed))

	var via map[string]string
	if err := chromedp.Run(runCtx, chromedp.Evaluate(jsMarkFields, &via)); err != nil {
		return zero, fmt.Errorf("browserauth: could not inspect the login form: %w", err)
	}
	var missing []string
	for _, f := range []string{"email", "password", "submit"} {
		if via[f] == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		a.dump(runCtx, "login-form")
		return zero, fmt.Errorf("browserauth: the login form changed, cannot find %s",
			strings.Join(missing, ", "))
	}
	for _, f := range []string{"email", "password", "submit"} {
		if via[f] != "id" {
			a.log.Warn("login control no longer has its usual id; found it by shape",
				"field", f, "via", via[f])
		}
	}

	if err := chromedp.Run(runCtx,
		chromedp.SendKeys(selEmail, cr.Email, chromedp.ByQuery),
		chromedp.SendKeys(selPassword, cr.Password, chromedp.ByQuery),
		chromedp.Click(selSubmit, chromedp.ByQuery),
	); err != nil {
		a.dump(runCtx, "login-submit")
		return zero, fmt.Errorf("browserauth: could not submit the login form: %w", err)
	}

	deviceID, deviceValue, deviceText := idJustOnce, valJustOnce, `["no recordar"]`
	if a.trustDevice {
		deviceID, deviceValue, deviceText = idTrustDevice, valTrustDevice, `["recordar este dispositivo"]`
	}

	deadline := time.Now().Add(a.timeout / 2)
	dumpedPrompt := false
	for {
		if time.Now().After(deadline) {
			a.dump(runCtx, "login-stuck")
			return zero, fmt.Errorf("browserauth: login did not finish in time (wrong credentials?)")
		}

		var href, body string
		if err := chromedp.Run(runCtx,
			chromedp.Location(&href),
			chromedp.Evaluate(`(document.body ? document.body.innerText : '').slice(0,4000).toLowerCase()`, &body),
		); err != nil {
			return zero, fmt.Errorf("browserauth: lost the page during login: %w", err)
		}

		if strings.Contains(href, box+".wodbuster.com") {
			break
		}
		if looksRejected(body) {
			return zero, fmt.Errorf("browserauth: WodBuster rejected the credentials")
		}
		if strings.Contains(body, "recordar este dispositivo") {
			var via string
			_ = chromedp.Run(runCtx, chromedp.Evaluate(
				fmt.Sprintf(jsPickDevice, deviceID, deviceValue, deviceText), &via))
			switch via {
			case "":
				// None of the three routes found the control. Record what the
				// page does offer, once: this step has already changed under
				// us once, and the dump is how we learn what it changed to.
				a.log.Warn("could not answer the device prompt",
					"id", deviceID, "value", deviceValue, "text", deviceText)
				if !dumpedPrompt {
					dumpedPrompt = true
					a.dump(runCtx, "device-prompt")
					var controls []map[string]string
					if err := chromedp.Run(runCtx,
						chromedp.Evaluate(jsDescribeClickables, &controls)); err == nil {
						a.log.Warn("clickables on the device prompt", "controls", controls)
					}
				}
			case "id":
				a.log.Debug("answered the device prompt", "id", deviceID)
			default:
				a.log.Warn("device control id is gone; found it another way",
					"id", deviceID, "via", via)
			}
		} else {
			a.log.Debug("waiting for the login to land", "url", href)
		}

		select {
		case <-runCtx.Done():
			return zero, runCtx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}

	var html string
	if err := chromedp.Run(runCtx,
		chromedp.Navigate("https://"+box+".wodbuster.com/athlete/reservas.aspx"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	); err != nil {
		return zero, fmt.Errorf("browserauth: could not load the booking page: %w", err)
	}
	m := reAthleteID.FindStringSubmatch(html)
	if m == nil {
		a.dump(runCtx, "no-athlete-id")
		return zero, fmt.Errorf("browserauth: could not find the athlete id on the booking page")
	}

	// Storage.getCookies, not Network.getCookies: the latter returns only what
	// the current page can see, which on the box subdomain leaves behind the
	// host-only cookies the login set on wodbuster.com itself.
	var jar []*network.Cookie
	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		jar, err = storage.GetCookies().Do(ctx)
		return err
	})); err != nil {
		return zero, fmt.Errorf("browserauth: could not read the session cookies: %w", err)
	}

	kept := toHTTPCookies(jar, box)
	if a.log.Enabled(runCtx, slog.LevelDebug) {
		names := make([]string, 0, len(kept))
		for _, c := range kept {
			names = append(names, c.Name)
		}
		a.log.Debug("session cookies", "in_jar", len(jar), "kept", names)
	}

	sess := wodbuster.Session{
		Box:       box,
		AthleteID: m[1],
		Cookies:   kept,
		IssuedAt:  time.Now(),
	}
	if err := sess.Valid(); err != nil {
		return zero, fmt.Errorf("browserauth: %w", err)
	}
	a.log.Info("authenticated", "box", box, "cookies", len(sess.Cookies))
	return sess, nil
}

// toHTTPCookies keeps every cookie for wodbuster.com and the box subdomain.
// The whole jar is kept on purpose: a login leaves several cookies and guessing
// which one matters works until it doesn't.
func toHTTPCookies(jar []*network.Cookie, box string) []*http.Cookie {
	hosts := []string{"wodbuster.com", box + ".wodbuster.com"}
	seen := map[string]bool{}
	var out []*http.Cookie
	for _, c := range jar {
		domain := strings.TrimPrefix(c.Domain, ".")
		keep := false
		for _, h := range hosts {
			if h == domain || strings.HasSuffix(h, "."+domain) {
				keep = true
				break
			}
		}
		if !keep {
			continue
		}
		key := c.Name + "\x00" + c.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HTTPOnly,
		})
	}
	return out
}

func looksRejected(body string) bool {
	for _, s := range []string{
		"la contraseña no es correcta",
		"contraseña incorrecta",
		"usuario o contraseña",
		"no existe ningún usuario",
		"el email no existe",
	} {
		if strings.Contains(body, s) {
			return true
		}
	}
	return false
}

var rePasswordValue = regexp.MustCompile(
	`(?i)(<input[^>]*type=["']?password["']?[^>]*?)\svalue=(?:"[^"]*"|'[^']*'|[^\s>]*)`)

// dump writes the current page and a screenshot so a broken login can be
// diagnosed after the fact. Passwords are stripped.
func (a *Authenticator) dump(ctx context.Context, name string) {
	if a.diagnosticsDir == "" {
		return
	}
	if err := os.MkdirAll(a.diagnosticsDir, 0o700); err != nil {
		a.log.Warn("could not create the diagnostics directory", "err", err)
		return
	}
	stamp := time.Now().Format("20060102-150405")
	base := fmt.Sprintf("%s/%s-%s", strings.TrimRight(a.diagnosticsDir, "/"), name, stamp)

	var html string
	var png []byte
	if err := chromedp.Run(ctx,
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.CaptureScreenshot(&png),
	); err != nil {
		a.log.Warn("could not capture diagnostics", "err", err)
		return
	}
	html = rePasswordValue.ReplaceAllString(html, "$1")
	if err := os.WriteFile(base+".html", []byte(html), 0o600); err != nil {
		a.log.Warn("could not write the diagnostics HTML", "err", err)
	}
	if err := os.WriteFile(base+".png", png, 0o600); err != nil {
		a.log.Warn("could not write the diagnostics screenshot", "err", err)
	}
	a.log.Warn("wrote login diagnostics", "html", base+".html", "png", base+".png")
}
