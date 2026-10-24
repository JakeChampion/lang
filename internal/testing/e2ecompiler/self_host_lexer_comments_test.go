package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/lexer"
)

const commentFixture = "// leading\nlet u: string = \"http://x//y\"; // trailing\nlet n: i32 = 1;\n//: last\n"

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
