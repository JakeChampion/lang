package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseEntryProgram = `import "std/i32";
struct Box { value: string, data: string[] }
enum Wrapped { Empty, Full(Box) }
@noinline function text(n: i32): string { return n.to_string() + "-payload"; }
@noinline function read(b: Box): i32 { return b.value.len() + b.data[0].len(); }
@noinline function keep(b: Box): Box { return b; }
@noinline function discard(b: Box): i32 { let held = keep(b); return read(held); }
@noinline function make(n: i32): Wrapped {
  let b = Box { value: text(n), data: [text(n + 1)] };
  if (discard(b) != 18) { return Wrapped.Empty; }
  let alias = b;
  if (read(alias) != 18) { return Wrapped.Empty; }
  return Wrapped.Full(b);
}
function main(): i32 {
  let static = Box { value: "a-payload", data: ["b-payload"] };
  let i: i32 = 0;
  while (i < 20) {
    if (discard(static) != 18 || read(static) != 18) { return 5; }
    let value = make(1);
    let saved = value;
    match (saved) { Wrapped.Empty => { return 1; }, Wrapped.Full(b) => { if (read(b) != 18) { return 2; } } }
    match (value) { Wrapped.Empty => { return 3; }, Wrapped.Full(b) => { if (read(b) != 18) { return 4; } } }
    i = i + 1;
  }
  return 0;
}
`

// Aliased records and enum payloads keep their children alive until the last
// release. Shared releases bypass the frame, while the debug build uses the
// ordinary helper so its diagnostics remain observable.
func TestSelfHostReleaseEntry(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(source, []byte(releaseEntryProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, debug := range []string{"0", "1"} {
			t.Run(target+"/debug="+debug, func(t *testing.T) {
				env := []string{"FERN_LEAKCHECK=1"}
				if debug == "1" {
					env = append(env, "FERN_SANITIZE=1")
				}
				assembly, err := os.ReadFile(cli.emit(t, source, target, env...))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(assembly), "_release_slow:") != (debug == "0") {
					t.Fatal("release entry guard does not respect debug mode")
				}
				if strings.Contains(string(assembly), "rcreld") {
					t.Fatal("release guard is duplicated at a call site")
				}
				stderr, code := cli.exitOf(t, releaseEntryProgram, target, env...)
				if code != 0 {
					t.Fatalf("exit %d\n%s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}
