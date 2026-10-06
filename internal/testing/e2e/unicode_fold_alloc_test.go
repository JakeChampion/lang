package e2e

import (
	"strings"
	"testing"
)

// to_lower / to_upper on ASCII input with nothing to remap hand the string
// back rather than copying it, so folding an already-folded header name or
// token allocates nothing; one letter to remap still builds a new string.
const unicodeFoldAllocSrc = `import "std/unicode";

function main(): i32 {
	let lower: string = "content-length";
	let upper: string = "GET";
	let before: i64 = __heap_alloc_count();
	let i: i32 = 0;
	while (i < 100) {
		if (unicode.to_lower(lower) != lower || unicode.to_upper(upper) != upper || unicode.to_lower("") != "") {
			return 10;
		}
		i = i + 1;
	}
	if (__heap_alloc_count() - before != (0 as i64)) {
		return 20;
	}
	let mixed: string = "Content-Length";
	before = __heap_alloc_count();
	if (unicode.to_lower(mixed) != "content-length") {
		return 30;
	}
	if (__heap_alloc_count() - before <= (0 as i64)) {
		return 31;
	}
	return 0;
}
`

func TestUnicodeFoldReturnsFoldedInputUncopied(t *testing.T) {
	out, code := compileAndRunX86_64(t, unicodeFoldAllocSrc)
	if code != 0 {
		t.Fatalf("exit %d, want 0 (20: an already-folded string was copied):\n%s", code, strings.TrimSpace(out))
	}
}
