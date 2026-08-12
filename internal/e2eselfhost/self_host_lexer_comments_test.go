package e2eselfhost

import (
	"testing"

	"github.com/jakechampion/lang/internal/lexer"
)

// The native half of the comment-capture differential. The self-host half is
// `test_comments` in examples/self_host/lexer.fern (exit codes 2100-2106),
// which TestSelfHostLexerX86_64 / TestSelfHostLexerArm64 already run on both
// backends. Both halves assert the SAME fixture and the SAME expectations, so
// either side drifting turns one of them red.
//
// Keep this fixture byte-identical to the one in test_comments.
const commentFixture = "// leading\nvar u: string = \"http://x//y\"; // trailing\nvar n: i32 = 1;\n//: last\n"

func TestLexerCommentsMatchSelfHost(t *testing.T) {
	_, comments, err := lexer.Tokenize(commentFixture)
	if err != nil {
		t.Fatalf("tokenize fixture: %v", err)
	}
	want := []struct {
		line, col int
		text      string
	}{
		{1, 1, " leading"},
		// Line 2 holds `"http://x//y"`. Both `//` runs inside the string
		// literal must be invisible here — a comment scan that does not
		// understand string literals reports four comments on this fixture,
		// not three, and that is the failure this fixture exists to catch.
		{2, 32, " trailing"},
		{4, 1, ": last"},
	}
	if len(comments) != len(want) {
		t.Fatalf("got %d comments, want %d: %+v", len(comments), len(want), comments)
	}
	for i, w := range want {
		got := comments[i]
		if got.Pos.Line != w.line || got.Pos.Col != w.col || got.Text != w.text {
			t.Errorf("comment %d: got %d:%d %q, want %d:%d %q",
				i, got.Pos.Line, got.Pos.Col, got.Text, w.line, w.col, w.text)
		}
	}
}
