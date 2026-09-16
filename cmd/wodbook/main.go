// Command wodbook books WodBuster classes the moment a week is published.
//
// Run it a few minutes before the opening; it authenticates, synchronises with
// the server's clock, waits, and books.
//
//	wodbook -config config.json            # wait for the opening, then book
//	wodbook -config config.json -dry       # resolve everything, book nothing
//	wodbook -config config.json -now       # skip the wait, act immediately
//	wodbook -config config.json -now -dry  # full dress rehearsal, any day
//
// Exit codes: 0 all good, 1 something failed, 2 bad config, 3 login failed.
//
// This binary is the library's proving ground. If it books, the library is
// right, and whatever consumes it next only has to get its own job right.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // zones baked in: no dependency on the host's tzdata

	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/browserauth"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster/race"
)

const version = "0.1.0"

const (
	exitOK       = 0
	exitFailed   = 1
	exitBadConf  = 2
	exitBadLogin = 3
)

func main() {
	var (
		configPath  = flag.String("config", "config.json", "path to the configuration file")
		dryRun      = flag.Bool("dry", false, "resolve everything but do not book")
		now         = flag.Bool("now", false, "do not wait for the opening, act immediately")
		verbose     = flag.Bool("v", false, "debug logging")
		showVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("wodbook", version)
		return
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid configuration:", err)
		os.Exit(exitBadConf)
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, cfg, log, *dryRun, *now))
}

func run(ctx context.Context, cfg *Config, log *slog.Logger, dryRun, skipWait bool) int {
	if dryRun {
		log.Info("dry run: nothing will be booked")
	}

	// 1. Authenticate. This is the slow part — 5-15 s and a browser — which is
	//    exactly why it happens before the window, never inside it.
	authOpts := []browserauth.Option{
		browserauth.WithHeadless(cfg.headless()),
		browserauth.WithTrustDevice(cfg.TrustDevice),
		browserauth.WithLogger(log),
	}
	if cfg.ChromePath != "" {
		authOpts = append(authOpts, browserauth.WithChromePath(cfg.ChromePath))
	}
	if cfg.DiagnosticsDir != "" {
		authOpts = append(authOpts, browserauth.WithDiagnosticsDir(cfg.DiagnosticsDir))
	}

	started := time.Now()
	sess, err := browserauth.New(authOpts...).Authenticate(ctx, cfg.Box,
		browserauth.Credentials{Email: cfg.Email, Password: cfg.Password})
	if err != nil {
		log.Error("login failed", "err", err)
		return exitBadLogin
	}
	// The password is not needed again; do not keep it around.
	cfg.Password = ""
	log.Info("logged in", "box", cfg.Box, "took", time.Since(started).Round(time.Millisecond))

	client, err := wodbuster.NewClient(sess, wodbuster.WithLogger(log))
	if err != nil {
		log.Error("could not build the client", "err", err)
		return exitBadLogin
	}

	// 2. Trust the server's clock, not this machine's.
	clock, err := wodbuster.NewServerClock(ctx, client, 4)
	if err != nil {
		log.Warn("could not sync with the server clock; falling back to the local one", "err", err)
		clock = wodbuster.SystemClock{}
	} else if oc, ok := clock.(wodbuster.OffsetClock); ok {
		log.Info("clock synced", "offset", oc.Offset.Round(time.Millisecond))
	}

	// 3. Resolve each recurring target to a concrete date.
	goals := make([]race.Goal, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		wd, _ := parseWeekday(t.Weekday)
		at, _ := wodbuster.ParseTimeOfDay(t.Time)
		waitlist := cfg.Waitlist
		if t.Waitlist != nil {
			waitlist = *t.Waitlist
		}
		g := race.Goal{
			Target: wodbuster.Target{
				Date:  wodbuster.NextWeekday(clock.Now().In(cfg.loc), wd),
				Start: at,
				Name:  t.Class,
			},
			Waitlist: waitlist,
		}
		goals = append(goals, g)
		log.Info("target", "class", g.Target.String(), "waitlist", waitlist)
	}

	opts := race.Options{
		Clock:       clock,
		PollEvery:   time.Duration(cfg.PollEveryMs) * time.Millisecond,
		StartBefore: time.Duration(cfg.StartBeforeMs) * time.Millisecond,
		GiveUpAfter: time.Duration(cfg.GiveUpAfterMs) * time.Millisecond,
		Attempts:    cfg.Attempts,
		DryRun:      dryRun,
		Log:         log,
	}

	// 4. Wait for the opening, unless told not to.
	if !skipWait {
		opensAt := cfg.nextOpening(clock.Now())
		log.Info("opening expected", "at", opensAt.Format(time.RFC1123),
			"in", time.Until(opensAt).Round(time.Second))

		// The server's own countdown beats a wall-clock guess when it offers one.
		if serverSays, err := race.OpensAt(ctx, client, goals[0].Date, clock); err == nil {
			if drift := serverSays.Sub(opensAt); drift > 2*time.Second || drift < -2*time.Second {
				log.Info("the server's countdown disagrees with the configured time; trusting the server",
					"server", serverSays.Format(time.RFC3339), "config", opensAt.Format(time.RFC3339))
				opensAt = serverSays
			}
		} else if !errors.Is(err, race.ErrNoCountdown) && !errors.Is(err, race.ErrAlreadyPublished) {
			log.Debug("could not read the server countdown", "err", err)
		}

		if err := race.WaitUntil(ctx, opensAt, opts); err != nil {
			log.Warn("cancelled before the opening", "err", err)
			return exitFailed
		}

		// One last cheap call: reopens any idle TLS connection so the first
		// request of the race does not pay a fresh handshake.
		if err := client.Ping(ctx); err != nil {
			log.Warn("warm-up ping failed", "err", err)
		}
	}

	// 5. Go.
	log.Info("chasing")
	results := race.Chase(ctx, client, goals, opts)

	failures := 0
	for _, r := range results {
		attrs := []any{"class", r.Goal.Target.String(), "outcome", r.Outcome.String()}
		if r.Class.ID != 0 {
			attrs = append(attrs, "id", int64(r.Class.ID), "free", r.Class.Free())
		}
		switch {
		case r.Outcome == race.OutcomeFailed:
			failures++
			log.Error("not booked", append(attrs, "err", r.Err)...)
		case r.Outcome == race.OutcomeWaitlisted:
			log.Warn("waiting list", attrs...)
		default:
			log.Info("done", attrs...)
		}
	}

	if failures > 0 {
		return exitFailed
	}
	return exitOK
}
