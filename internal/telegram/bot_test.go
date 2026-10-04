package telegram

import "testing"

// The bot logs every command it receives. /login carries a password, and a
// password in the container logs is the one mistake this bot cannot make.
func TestRedactArgsKeepsPasswordsOutOfTheLogs(t *testing.T) {
	for name, tc := range map[string]struct {
		command string
		args    string
		want    string
	}{
		"login hides the password":   {"login", "athlete@example.com hunter2", "athlete@example.com <redacted>"},
		"login with only a password": {"login", "hunter2", "hunter2 <redacted>"},
		"login with trailing spaces": {"login", "  a@b.c   hunter2  ", "a@b.c <redacted>"},
		"book has nothing to hide":   {"book", "Monday 07:00 wod", "Monday 07:00 wod"},
		"no arguments":               {"status", "", ""},
		"unknown command with args":  {"wat", "something", "something"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := redactArgs(tc.command, tc.args); got != tc.want {
				t.Errorf("redactArgs(%q, %q) = %q, want %q", tc.command, tc.args, got, tc.want)
			}
		})
	}

	// Whatever shape the arguments take, the second field never survives.
	if got := redactArgs("login", "a@b.c my password with spaces"); got != "a@b.c <redacted>" {
		t.Errorf("a password containing spaces leaked: %q", got)
	}
}

func TestFirstLineKeepsRepliesShort(t *testing.T) {
	if got := firstLine("one\ntwo\nthree"); got != "one" {
		t.Errorf("firstLine = %q, want %q", got, "one")
	}
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	if got := firstLine(string(long)); len(got) != 83 {
		t.Errorf("long line not truncated: %d chars", len(got))
	}
}
