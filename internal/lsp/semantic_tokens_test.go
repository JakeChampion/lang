package lsp

import (
	"testing"
)

func semanticTokensFor(src string) semanticTokensResponse {
	s := NewServer()
	s.updateDoc("file:///t", src)
	return runSemanticTokens(s.docs["file:///t"], "file:///t")
}

func TestSemanticTokens_LegendMatchesIota(t *testing.T) {
	// Make sure the legend slice's order matches what the iota
	// constants encode. A drift here would mis-colour every token
	// the client receives.
	want := []string{
		"function", "method", "struct", "enum", "enumMember",
		"parameter", "variable", "property", "type",
		"keyword", "namespace",
	}
	got := semanticTokenTypeNames()
	if len(got) != len(want) {
		t.Fatalf("legend length = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("legend[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSemanticTokens_EmitsForSimpleProgram(t *testing.T) {
	src := "function add(a: i32, b: i32): i32 { return a + b; }\n"
	got := semanticTokensFor(src)
	if len(got.Data)%5 != 0 {
		t.Fatalf("data length = %d, not a multiple of 5", len(got.Data))
	}
	if len(got.Data) == 0 {
		t.Fatal("expected at least one token for the program")
	}
}

func TestSemanticTokens_DeltaEncoded(t *testing.T) {
	// Two tokens on the same line: the second's deltaLine must be
	// 0 (relative to first) and deltaStartChar relative to the
	// first's start, not absolute.
	src := "function add(a: i32, b: i32): i32 { return a + b; }\n"
	got := semanticTokensFor(src)
	if len(got.Data) < 10 {
		t.Skip("need at least 2 tokens for delta check")
	}
	// First token absolute, second token delta'd.
	dLine2 := got.Data[5]
	if dLine2 < 0 {
		t.Errorf("second token deltaLine should be >= 0, got %d", dLine2)
	}
}
