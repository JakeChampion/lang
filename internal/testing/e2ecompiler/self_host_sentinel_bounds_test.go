package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sentinelBoundsFunctions = `
const TABLE: i32[] = [30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45];
@noinline function filtered(x: i32): i32 {
 let at = if (x < 0) { -1 } else { x & 15 };
 if (at < 0) { return 77; }
 return TABLE[at];
}
@noinline function unfiltered(x: i32): i32 {
 let at = if (x < 0) { -1 } else { x & 15 };
 return TABLE[at];
}
@noinline function oversized(x: i32): i32 {
 let at = if (x < 0) { -1 } else { x & 31 };
 if (at < 0) { return 77; }
 return TABLE[at];
}
@noinline function empty(x: i32): i32 {
 let xs: i32[] = [];
 let at = if (x < 0) { -1 } else { x & 15 };
 if (at < 0) { return 77; }
 return xs[at];
}
@noinline function wrapped(x: i32): i32 {
 let at = if (x < 0) { -1 } else { (x & 1) * 2147483647 + 16 };
 if (at < 0) { return 77; }
 return TABLE[at];
}
`

func TestSelfHostSentinelBounds(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const valid = `function main(): i32 {
  for x in -32..64 {
   let expected = if (x < 0) { 77 } else { 30 + (x & 15) };
   if (filtered(x) != expected) { return 1; }
  }
  if (unfiltered(15) != 45 || oversized(15) != 45 || empty(-1) != 77 || wrapped(1) != 77) { return 2; }
  return 0;
 }`
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, sentinelBoundsFunctions+valid, target); code != 0 {
				t.Fatalf("valid sentinel access: exit %d\n%s", code, stderr)
			}
			for _, call := range []string{"unfiltered(-1)", "oversized(16)", "empty(0)", "wrapped(0)"} {
				t.Run(call, func(t *testing.T) {
					stderr, code := cli.exitOf(t, sentinelBoundsFunctions+"function main(): i32 { return "+call+"; }", target)
					if code == 0 || !strings.Contains(stderr, "out of range") {
						t.Fatalf("required bounds trap: exit %d\n%s", code, stderr)
					}
				})
			}
		})
	}
}

func TestSelfHostSentinelBoundsShape(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "sentinel.fern")
	source := sentinelBoundsFunctions + `function main(): i32 { return filtered(1)+unfiltered(1)+oversized(1)+empty(-1)+wrapped(1); }`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			data, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			// Include every return path: the sentinel rejection returns before the lookup.
			for _, name := range []string{"filtered", "unfiltered", "oversized", "empty", "wrapped"} {
				start := strings.Index(text, "\n__fn_"+name+":\n")
				if start < 0 {
					t.Fatalf("missing %s", name)
				}
				body := text[start:]
				end := strings.Index(body, ".cfi_endproc")
				if end < 0 {
					t.Fatalf("missing boundary for %s", name)
				}
				body = body[:end]
				hasCheck := strings.Contains(body, "__fern_oob_abort")
				if hasCheck != (name != "filtered") {
					t.Errorf("%s unexpected bounds check state:\n%s", name, body)
				}
			}
		})
	}
}
