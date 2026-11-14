package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func arrayJoinLengthValidSource() string {
	var source strings.Builder
	source.WriteString(`import "std/array";
import "std/string";
@noinline function joined(input: string[], sep: string, last: string, special: boolean): string {
  let heap: string[] = [];
  for item in input { heap = heap.append(item.repeat(2)); }
  return if (special) { heap.join_with_last(sep, last) } else { heap.join(sep) };
}
@noinline function check(input: string[], sep: string, last: string, special: boolean, want: string): boolean {
  let result = joined(input, sep, last, special);
  let churn = "heap churn".repeat(32);
  return churn.len() == 320 && result == want;
}
function main(): i32 {
`)
	index := 0
	for _, input := range [][]string{nil, {""}, {"a"}, {"", ""}, {"a", "b"}, {"a", "", "b"}, {"é", "𝄞", "🙂"}, {"a\x00", "b", "\x00"}} {
		quoted, doubled := []string{}, []string{}
		for _, item := range input {
			quoted = append(quoted, strconv.Quote(item))
			doubled = append(doubled, item+item)
		}
		for _, sep := range []string{"", ":", "𝄞\x00"} {
			for _, mode := range []struct {
				special bool
				last    string
			}{{false, ""}, {true, ""}, {true, " and "}, {true, "é\x00"}} {
				want := strings.Join(doubled, sep)
				if mode.special && len(doubled) >= 2 {
					want = strings.Join(doubled[:len(doubled)-1], sep) + mode.last + doubled[len(doubled)-1]
				}
				index++
				fmt.Fprintf(&source, "  if (!check([%s], %s, %s, %t, %s)) { return %d; }\n", strings.Join(quoted, ","), strconv.Quote(sep), strconv.Quote(mode.last), mode.special, strconv.Quote(want), index)
			}
		}
	}
	source.WriteString(`  let big = "x".repeat(1048576);
  let empty: string[] = [];
  if (empty.join(big) != "" || empty.join_with_last(big, big) != "") { return 97; }
  if (["a"].join(big) != "a" || ["a"].join_with_last(big, big) != "a") { return 98; }
  if (["a", "b"].join_with_last(big, ":") != "a:b") { return 99; }
  let a = [big, ""].join("");
  if (a.len() != 1048576 || a[1048575] != b'x') { return 100; }
  let b = ["", "", ""].join_with_last(big, "!");
  if (b.len() != 1048577 || b[1048575] != b'x' || b[1048576] != b'!') { return 101; }
  print("join lengths ok");
  return 0;
}
`)
	return source.String()
}

func TestArrayJoinLengthValid(t *testing.T) {
	for _, target := range []string{"oracle", "x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			out, code := runStringByteLength(t, target, arrayJoinLengthValidSource())
			if code != 0 || out != "join lengths ok\n" {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
}

func arrayJoinLengthOverflowSources() []struct{ name, source string } {
	cases := []struct {
		name, piece, sep, last string
		count                  int
		special                bool
	}{
		{"separator_zero", `""`, `"x".repeat(2097152)`, `""`, 2049, false},
		{"elements_zero", `"x".repeat(2097152)`, `""`, `""`, 2048, false},
		{"combined_small", `"x".repeat(2097152)`, `"x".repeat(2097152)`, `""`, 1025, false},
		{"signed_min", `""`, `"x".repeat(2097152)`, `""`, 1025, false},
		{"last_separator_zero", `""`, `"x".repeat(2097152)`, `""`, 2050, true},
		{"last_tail_zero", `""`, `"x".repeat(2097152)`, `"x".repeat(2097152)`, 2049, true},
		{"last_elements_zero", `"x".repeat(2097152)`, `""`, `""`, 2048, true},
		{"last_sum_small", `"a"`, `"x".repeat(2097152)`, `""`, 2050, true},
	}
	result := []struct{ name, source string }{}
	for _, tc := range cases {
		call := "items.join(sep)"
		if tc.special {
			call = "items.join_with_last(sep, last)"
		}
		source := fmt.Sprintf(`import "std/array";
import "std/string";
@noinline function count(): i32 {
  let piece = %s;
  let sep = %s;
  let last = %s;
  let items: string[] = [];
  for i in 0..%d { items = items.append(piece); }
  return %s.len();
}
function main(): i32 { print(count()); return 0; }
`, tc.piece, tc.sep, tc.last, tc.count, call)
		result = append(result, struct{ name, source string }{tc.name, source})
	}
	return result
}

// The native heap and Wasm memory limits also bound the old, unchecked path.
func runArrayJoinLengthBounded(t *testing.T, target, source string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	entry, out := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main")
	if err := os.WriteFile(entry, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var runner []string
	switch target {
	case "x86-64-linux":
		runner = e2eharness.X86_64Runner(t)
	case "arm64-linux":
		if qemu := e2eharness.Arm64Runner(t); qemu != "" {
			runner = []string{qemu}
		}
	default:
		runner = []string{e2eharness.Wasmtime(t), "run", "-W", "max-memory-size=67108864"}
	}
	cmd := e2eharness.SelfHostCompileCmd(t, target, entry, out)
	flags := []string{"-heap-limit", "64M"}
	if target == "wasm32-wasi" {
		flags = []string{"-emit", "core-module"}
	}
	cmd.Args = append(cmd.Args[:1], append(flags, cmd.Args[1:]...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	args := append(runner, out)
	run := exec.CommandContext(ctx, args[0], args[1:]...)
	output, _ := run.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("runtime budget: %v\n%s", ctx.Err(), output)
	}
	if run.ProcessState == nil {
		t.Fatalf("runtime did not start: %s", output)
	}
	return string(output), run.ProcessState.ExitCode()
}

func TestArrayJoinLengthOverflowCompiled(t *testing.T) {
	for _, tc := range arrayJoinLengthOverflowSources() {
		t.Run(tc.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					out, code := runArrayJoinLengthBounded(t, target, tc.source)
					if code == 0 || !strings.Contains(out, "allocation size out of range") || strings.TrimSpace(withoutAbortDiag(out)) != "" {
						t.Fatalf("exit %d: %s", code, out)
					}
				})
			}
		})
	}
}

func TestArrayJoinLastLengthOverflowInterp(t *testing.T) {
	// The old join path can grow an unbounded Go buffer. These last-separator
	// cases instead reach a bounded index guard before the output can grow.
	for _, tc := range arrayJoinLengthOverflowSources()[4:] {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runInterpExitCode(t, tc.source)
			if code == 0 || !strings.Contains(out, "allocation size out of range") || strings.TrimSpace(withoutAbortDiag(out)) != "" {
				t.Fatalf("exit %d: %s", code, out)
			}
		})
	}
}
