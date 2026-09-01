package suggest

import "testing"

// The algorithm's own tests, moved here with it from internal/native/x86_64
// when the arm64 assembler gained suggestions too.
func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want int
	}{
		{"mov", "mov", 2, 0},
		{"mvo", "mov", 2, 1}, // adjacent transposition
		{"vaddpd", "addpd", 2, 1},
		{"ad", "add", 2, 1},
		{"frobnicate", "mov", 2, 3}, // capped at max+1
	}
	for _, c := range cases {
		if got := Distance(c.a, c.b, c.max); got != c.want {
			t.Errorf("Distance(%q, %q, %d) = %d, want %d", c.a, c.b, c.max, got, c.want)
		}
	}
}

// TestClosest pins the two rules that decide whether a suggestion is offered
// at all: a one-character input never gets one, and the edit budget widens
// from one to two once the input is longer than four characters.
func TestClosest(t *testing.T) {
	vocab := []string{"add", "adds", "ldr", "ldur", "movz", "stp", "sub"}
	cases := []struct{ in, want string }{
		{"ads", "add"},    // one edit, short name
		{"x", ""},         // too short to guess from
		{"zzzzzz", ""},    // nothing within budget
		{"movzz", "movz"}, // two edits allowed past four characters
		{"str", "stp"},    // one edit
	}
	for _, c := range cases {
		if got := Closest(c.in, vocab); got != c.want {
			t.Errorf("Closest(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
