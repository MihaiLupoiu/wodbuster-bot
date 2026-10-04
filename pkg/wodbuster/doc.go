// Package wodbuster is a client for WodBuster's class booking, built for the
// case that actually matters: winning the rush when a gym publishes its week.
//
// # Shape
//
// Authenticating and booking are separate concerns and are separate packages.
// This one speaks HTTP and depends on nothing outside the standard library;
// getting a Session is the job of an Authenticator, and the one that drives a
// headless browser lives in ./browserauth so that callers who only read a
// calendar do not pull a browser into their build.
//
//	sess, err := browserauth.New().Authenticate(ctx, "firespain", creds)
//	client, err := wodbuster.NewClient(sess)
//	day, err := client.Schedule(ctx, wodbuster.NextWeekday(time.Now(), time.Monday))
//	class, err := day.Resolve(target)
//	err = client.Book(ctx, class.ID, class.Date)
//
// # Two identities
//
// A class has a persistent identity — Target, being a date, a start time and a
// name — and an ephemeral one, ClassID. The ids do not exist until the box
// publishes the week and are different the week after, so:
//
//	A ClassID written to disk is a bug.
//
// Persist Targets. Resolve them to ids inside the booking window, use the id
// immediately, throw it away.
//
// # What this package does not do
//
// Every method is one HTTP request. It never retries, never sleeps, never polls
// and never re-authenticates. When a session dies it says so with
// ErrSessionExpired and stops; deciding what to do about that needs the
// caller's credentials, storage and rate limits, none of which belong here.
//
// Timing policy is the same story: ./race has the waiting and polling built on
// these primitives, and a caller who wants different policy can ignore it.
//
// # Clocks
//
// Do not trust the local clock for the booking moment. NewServerClock measures
// the offset against the server, and Schedule reports the server's own
// countdown in Schedule.OpensIn. Both exist because a booking window is decided
// in milliseconds and machine clocks are routinely off by more than that.
package wodbuster
