package metrics

// LabelOthers is where unbounded label values are bucketed.
//
// A label value that comes from outside — a Telegram message, an athlete id, a
// server's error text — can take any number of forms, and every distinct one is
// a time series Prometheus keeps. Same convention as buying-engine-service.
const LabelOthers = "(others)"

// LabelNone marks the absence of a value, kept separate from LabelOthers
// because "no command at all" and "a command we do not know" are different
// facts about the traffic.
const LabelNone = "(none)"

// Bounded keeps a label value inside a known set: anything else becomes
// "(others)", and an empty value becomes "(none)".
func Bounded(value string, allowed map[string]bool) string {
	switch {
	case value == "":
		return LabelNone
	case allowed[value]:
		return value
	default:
		return LabelOthers
	}
}
