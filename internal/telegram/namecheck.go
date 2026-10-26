package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/booking"
	"github.com/MihaiLupoiu/wodbuster-bot/pkg/wodbuster"
)

// nameCheckDelay is how long after the last /book the check runs. Athletes add
// a week's classes in a burst, and every check costs a login through the same
// queue the Sunday run uses, so one check covers the whole burst.
const nameCheckDelay = 30 * time.Second

// nameChecker compares an athlete's classes with the box's published week a
// little after they /book, and warns about any name the box does not have.
//
// /book cannot refuse a name: the week it is for is usually not published yet.
// But the box rarely renames classes, so a name missing from this week is
// almost always a typo or a short form — "Open" for "Open box" — and finding
// that out on Monday beats finding it out at noon on Sunday.
type nameChecker struct {
	delay    time.Duration
	busy     func() bool // a booking run is in progress; logins are its alone
	rehearse func(ctx context.Context, chatID int64) ([]booking.Outcome, error)
	notify   func(chatID int64, text string)
	log      *slog.Logger

	mu     sync.Mutex
	timers map[int64]*time.Timer
}

// schedule (re)starts the athlete's countdown.
func (c *nameChecker) schedule(chatID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.timers[chatID]; ok {
		t.Stop()
	}
	c.timers[chatID] = time.AfterFunc(c.delay, func() { c.run(chatID) })
}

func (c *nameChecker) run(chatID int64) {
	c.mu.Lock()
	delete(c.timers, chatID)
	c.mu.Unlock()

	if c.busy() {
		c.log.Info("skipping the class name check during a booking run", "chat_id", chatID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	outcomes, err := c.rehearse(ctx, chatID)
	if err != nil {
		c.log.Warn("class name check failed", "chat_id", chatID, "err", err)
		return
	}
	if msg := nameCheckMessage(outcomes); msg != "" {
		c.notify(chatID, msg)
	}
}

// nameCheckMessage is empty unless a class was not found. Any other failure —
// an unpublished day, a full class — says nothing about the name.
func nameCheckMessage(outcomes []booking.Outcome) string {
	var lines []string
	for _, o := range outcomes {
		if errors.Is(o.Err, wodbuster.ErrClassNotFound) {
			lines = append(lines, formatOutcome(o))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "⚠️ I checked your classes against this week's schedule and the box has no class by " +
		"these names:\n\n" + strings.Join(lines, "\n") + "\n\n" +
		"The name has to match the box's exactly, or the Sunday run cannot find it. " +
		"To fix it, use /reset and add your classes again with the right names."
}
