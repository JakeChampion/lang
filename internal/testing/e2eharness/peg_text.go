package e2eharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// WritePegTextFixture compares every byte range of mixed-width text with
// Go's independent UTF-8 validator, including empty interior ranges.
func WritePegTextFixture(t testing.TB) string {
	t.Helper()
	var source strings.Builder
	source.WriteString(`import "std/peg";
import "std/utf8";

function any_n(n: i32): peg.Pattern {
  let parts: peg.Pattern[] = [];
  let i: i32 = 0;
  while (i < n) { parts = parts.append(PAny); i = i + 1; }
  return PSeq(parts);
}

// reached is where a match ended, or how far a failed one got.
function reached(r: Result[peg.PegMatch, peg.PegError]): i32 {
  match (r) {
    Ok(m) => { return m.pos; },
    Err(e) => { return e.furthest; }
  }
  return 0;
}

function fails_at(r: Result[peg.PegMatch, peg.PegError], at: i32): boolean {
  if let Err(e) = r { return e.furthest == at; }
  return false;
}

function check(input: string, a: i32, b: i32, valid: boolean, want: string): boolean {
  let p: peg.Pattern = PSeq([any_n(a), PCap("value", any_n(b - a)), PStar(PAny), PEof]);
  let r: Result[peg.PegMatch, peg.PegError] = peg.peg_match_pattern(p, input);
  if (reached(r) != input.len()) { return false; }
  let Ok(m) = r else { return !valid; };
  if (!valid) { return false; }
  match (m.caps.get("value")) {
    Some(s) => { return s == want && utf8.is_valid_utf8(s); },
    None => { return false; }
  }
}

function special(): boolean {
  // Matching remains byte-oriented when no text capture is requested.
  let Ok(bare) = peg.peg_match_pattern(PAny, "é") else { return false; };
  if (bare.pos != 1 || bare.caps.len() != 0) { return false; }
  let Ok(full) = peg.peg_match_pattern(PCap("v", PSeq([PAny, PAny])), "é") else { return false; };
  if (full.pos != 2 || full.caps.get_or("v", "") != "é") { return false; }
  // Validation is after committed ordered choice, without retrying it.
  let choice: peg.Pattern = PChoice([PCap("v", PAny), PCap("v", PLit("é"))]);
  if (!fails_at(peg.peg_match_pattern(choice, "é"), 1)) { return false; }
  // Captures in a failed alternative are discarded before validation.
  let discarded: peg.Pattern = PChoice([PSeq([PCap("bad", PAny), PLit("x")]), PCap("v", PLit("é"))]);
  let Ok(good) = peg.peg_match_pattern(discarded, "é") else { return false; };
  if (good.caps.has("bad") || good.caps.get_or("v", "") != "é") { return false; }
  // A later valid capture must not expose earlier invalid text, even when
  // both captures use the same name.
  let repeated: peg.Pattern = PSeq([PCap("v", PAny), PAny, PCap("v", PLit("a"))]);
  if (!fails_at(peg.peg_match_pattern(repeated, "éa"), 3)) { return false; }
  let names: peg.Pattern = PSeq([PCap("v", PLit("é")), PCap("v", PLit("a"))]);
  let Ok(last) = peg.peg_match_pattern(names, "éa") else { return false; };
  if (last.caps.len() != 1 || last.caps.get_or("v", "") != "a") { return false; }
  // Preserve the furthest watermark from failed alternatives.
  let furthest: peg.Pattern = PChoice([PSeq([PLit("é"), PLit("x")]), PCap("v", PAny)]);
  if (!fails_at(peg.peg_match_pattern(furthest, "éa"), 2)) { return false; }
  let rules: Map[string, peg.Pattern] = map_new(2);
  rules = rules.insert("root", PCap("all", PRef("rest")));
  rules = rules.insert("rest", PChoice([PSeq([PAny, PRef("rest")]), PEof]));
  let Ok(g) = peg.peg_grammar(rules, "root") else { return false; };
  let Ok(recursive) = peg.peg_match(g, "é€𐐀") else { return false; };
  return recursive.caps.get_or("all", "") == "é€𐐀";
}

function main(): i32 {
  if (!fails_at(peg.peg_match_pattern(PCap("v", PAny), "é"), 1)) {
    print("partial scalar capture accepted"); return 1;
  }
  if (!special()) { print("capture semantics"); return 2; }
`)
	quote := func(s string) string {
		return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\x00", "\\0").Replace(s) + "\""
	}
	for _, input := range []string{"", "A\x00¢€𐐀\U0010ffffZ"} {
		for start := 0; start <= len(input); start++ {
			for end := start; end <= len(input); end++ {
				part := input[start:end]
				valid := utf8.ValidString(part)
				want := ""
				if valid {
					want = part
				}
				fmt.Fprintf(&source, "  if (!check(%s, %d, %d, %t, %s)) { print(\"range %d:%d\"); return 3; }\n", quote(input), start, end, valid, quote(want), start, end)
			}
		}
	}
	source.WriteString("  return 0;\n}\n")
	path := filepath.Join(t.TempDir(), "peg_text.fern")
	if err := os.WriteFile(path, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
