package booking

import (
	"testing"
	"time"
)

func TestWeekdayPriorityBooksMondayFirst(t *testing.T) {
	order := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday,
		time.Friday, time.Saturday, time.Sunday}
	for want, d := range order {
		if got := weekdayPriority(d); got != want {
			t.Errorf("%s: priority %d, want %d", d, got, want)
		}
	}
}
