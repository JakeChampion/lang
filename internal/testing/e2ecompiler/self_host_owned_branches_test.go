package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostOwnedExclusiveBranches(t *testing.T) {
	cli := buildSelfHostCLI(t)
	path := filepath.Join(e2eharness.RepoPath(), "internal/check/checker/testdata/owned_branches.fern")
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			var allocs, frees, live int64
			summary := leakSummaryLine(stderr)
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse census %q: %v", summary, err)
			}
			if allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("invalid census: %s", summary)
			}
		})
	}
}

func TestSelfHostOwnedBranchErrors(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const prefix = `struct Box { data: i32[] }
struct Wrap { state: Box }
function consume(own box: Box): i32 { return box.data[0]; }
function keep(own box: Box): Box { return box; }
function guard(own box: Box): boolean { return box.data[0] == 0; }
enum Choice { Wrapped(Box), Empty }
struct Callable { Wrapped: (Box) => Box }
`
	for _, tc := range []struct{ name, source, code string }{
		{"post expression", `function f(own box: Box, tag: i32): i32 { let n = match (tag) { 0 => consume(box), _ => 0 }; return n + consume(box); }`, "E050"},
		{"expression order", `function f(own box: Box, tag: i32): i32 { return (match (tag) { 0 => consume(box), _ => 0 }) + box.data[0]; }`, "E050"},
		{"post conditional", `function f(own box: Box, flag: boolean): i32 { let n = if (flag) { consume(box) } else { 0 }; return n + consume(box); }`, "E050"},
		{"post nested value", `function f(own box: Box, flag: boolean): i32 { let n = if (flag) { if (flag) { consume(box) } else { 0 } } else { 0 }; return n + consume(box); }`, "E050"},
		{"post value block", `function f(own box: Box): i32 { let n = { consume(box) }; return n + consume(box); }`, "E050"},
		{"guard fallthrough", `function f(own box: Box, tag: i32): i32 { match (tag) { 0 when guard(box) => { return 0; }, _ => { return consume(box); } } }`, "E050"},
		{"borrowed guard", `function f(box: Box, tag: i32): i32 { match (tag) { 0 when guard(box) => { return 1; }, _ => { return 0; } } }`, "E051"},
		{"borrowed return", `function f(box: Box, flag: boolean): Wrap { if (flag) { return Wrap { state: keep(box) }; } return Wrap { state: box }; }`, "E051"},
		{"borrowed payload", `function f(choice: Choice): i32 { return match (choice) { Choice.Wrapped(item) => consume(item), Choice.Empty => 0 }; }`, "E051"},
		{"shadowed constructor", `function f(box: Box): i32 { let Choice = Callable { Wrapped: (value: Box): Box => value }; return consume(Choice.Wrapped(box)); }`, "E051"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(prefix+tc.source+"\nfunction main(): i32 { return 0; }\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					output, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "out"), path).CombinedOutput()
					if err == nil || !strings.Contains(string(output), "error["+tc.code+"]") {
						t.Fatalf("want %s, got %v: %s", tc.code, err, output)
					}
				})
			}
		})
	}
}
