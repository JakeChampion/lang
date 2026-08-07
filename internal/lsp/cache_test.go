package lsp

import (
	"encoding/json"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// withParseCounter wraps the package parseFor with a counter so tests
// can assert that the parse-stage was skipped via the cache or the
// same-source fast path. Returns a cleanup that restores the original.
func withParseCounter(t *testing.T, count *int) func() {
	t.Helper()
	orig := parseFor
	parseFor = func(src string) (*ast.Program, error) {
		*count++
		return orig(src)
	}
	return func() { parseFor = orig }
}

func TestUpdateDoc_IdenticalContentSkipsParse(t *testing.T) {
	src := "function main(): i32 { return 0; }\n"
	s := NewServer()
	s.SetPublisher(func(string, any) {})

	var parses int
	defer withParseCounter(t, &parses)()

	s.updateDoc("file:///t", src)
	if parses != 1 {
		t.Fatalf("first update should parse once, got %d", parses)
	}
	// Re-update with the same source: same URI, no source change.
	// The early-return guard short-circuits before any cache work.
	s.updateDoc("file:///t", src)
	if parses != 1 {
		t.Errorf("re-update with identical source should skip parse, got %d", parses)
	}
}

func TestUpdateDoc_CacheHitAcrossURIs(t *testing.T) {
	// Same content under a different URI exercises the cross-doc
	// cache — useful for editors that briefly close + reopen a
	// file, or for snippet templates pasted into multiple buffers.
	src := "function main(): i32 { return 7; }\n"
	s := NewServer()
	s.SetPublisher(func(string, any) {})

	var parses int
	defer withParseCounter(t, &parses)()

	s.updateDoc("file:///a", src)
	s.updateDoc("file:///b", src)
	if parses != 1 {
		t.Errorf("identical source under two URIs should parse once, got %d", parses)
	}
	// Both docs should be fully populated with the same products.
	a := s.docs["file:///a"]
	b := s.docs["file:///b"]
	if a == nil || b == nil {
		t.Fatalf("both docs should be present, got a=%v b=%v", a, b)
	}
	if a.prog != b.prog || a.info != b.info {
		t.Errorf("cached entries should share prog + info pointers")
	}
}

func TestUpdateDoc_CacheEviction(t *testing.T) {
	// Push past the cache cap and verify the oldest entry was
	// evicted (re-parses) while the newest survives.
	const cap = 4
	s := NewServer()
	s.cache = newCompileCache(cap)
	s.SetPublisher(func(string, any) {})

	var parses int
	defer withParseCounter(t, &parses)()

	makeSrc := func(n int) string {
		// Each source differs by the constant in the return so
		// hash + content compare differ deterministically.
		return "function main(): i32 { return " + itoa(n) + "; }\n"
	}
	for i := 0; i < cap; i++ {
		s.updateDoc("file:///t", makeSrc(i))
	}
	if parses != cap {
		t.Fatalf("seeding cap=%d distinct sources should parse %d times, got %d", cap, cap, parses)
	}
	// One more push: the oldest (i=0) should be evicted.
	s.updateDoc("file:///t", makeSrc(cap))
	if parses != cap+1 {
		t.Errorf("cap-th distinct source should parse, got %d", parses)
	}
	// Revisit the EVICTED source — should reparse.
	s.updateDoc("file:///t", makeSrc(0))
	if parses != cap+2 {
		t.Errorf("evicted source should re-parse, got %d", parses)
	}
	// Revisit a SURVIVING source — should NOT reparse.
	s.updateDoc("file:///t", makeSrc(cap))
	if parses != cap+2 {
		t.Errorf("cached survivor should not re-parse, got %d", parses)
	}
}

func TestPublishDiagnostics_FirstPublishAlwaysFiresEvenWhenEmpty(t *testing.T) {
	// Regression for the playground "clean source clears the
	// Problems panel" test: a clean document's didOpen must emit
	// publishDiagnostics([]) so the client knows to render the
	// no-problems state. The dedup check eats this if lastDiags[uri]
	// defaults to nil, because diagnosticsEqual treats nil as equal to
	// []Diagnostic{}.
	src := "function main(): i32 { return 0; }\n"
	s := NewServer()

	var pubCount int
	s.SetPublisher(func(method string, params any) {
		if method == "textDocument/publishDiagnostics" {
			pubCount++
		}
	})

	openMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{
			TextDocument: textDocumentItem{
				URI:        "file:///clean.fern",
				LanguageID: "fern",
				Text:       src,
			},
		}),
	})
	s.HandleMessage(openMsg)
	if pubCount != 1 {
		t.Errorf("clean document's first didOpen should still publish; got pubCount=%d", pubCount)
	}
}

func TestPublishDiagnostics_DedupsIdentical(t *testing.T) {
	src := "function main(): i32 { return undeclared; }\n"
	s := NewServer()

	var pubCount int
	s.SetPublisher(func(method string, params any) {
		if method == "textDocument/publishDiagnostics" {
			pubCount++
		}
	})

	openMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didOpen",
		Params: jsonRaw(didOpenParams{
			TextDocument: textDocumentItem{
				URI:        "file:///d.fern",
				LanguageID: "fern",
				Text:       src,
			},
		}),
	})
	s.HandleMessage(openMsg)
	// Now send didChange with the SAME source — diagnostics
	// shouldn't move.
	var ch didChangeParams
	ch.TextDocument.URI = "file:///d.fern"
	ch.TextDocument.Version = 2
	ch.ContentChanges = []contentChange{{Text: src}}
	changeMsg, _ := json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didChange",
		Params:  jsonRaw(ch),
	})
	s.HandleMessage(changeMsg)
	if pubCount != 1 {
		t.Errorf("identical didChange should not republish; pubCount=%d", pubCount)
	}
	// Change to a different (clean) source — should publish the new
	// empty diagnostic list.
	ch.ContentChanges = []contentChange{{Text: "function main(): i32 { return 0; }\n"}}
	changeMsg, _ = json.Marshal(message{
		Jsonrpc: "2.0",
		Method:  "textDocument/didChange",
		Params:  jsonRaw(ch),
	})
	s.HandleMessage(changeMsg)
	if pubCount != 2 {
		t.Errorf("change with new diagnostics should publish; pubCount=%d", pubCount)
	}
}

// itoa is a tiny base-10 int formatter — avoids pulling strconv in
// for the single use in TestUpdateDoc_CacheEviction's source builder.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
