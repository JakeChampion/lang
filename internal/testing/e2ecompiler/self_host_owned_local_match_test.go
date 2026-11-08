package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ownedLocalMatchDecls = `struct Window { bytes: u8[], ready: boolean }
struct Index { entries: i64[], limit: i32 }
struct Cursor { index: Index, entry: i32, target: i64, window: Option[Window] }
function take(own index: Index, entry: i32, target: i64, own w: Window): Result[(Cursor, i32), string] { return Ok((Cursor { index: index, entry: entry, target: target, window: None }, w.bytes.len())); }
function make(offset: i64, limit: i32): Result[Window, string] { return Ok(Window { bytes: [7u8], ready: false }); }
function (own w: Window) next(): Result[Window, string] { return Ok(w); }
function consume(own w: Window): i32 { return w.bytes.len(); }
function borrowed(w: Window): Window { return w; }
`

func ownedLocalMatchCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct {
		name, params, result, body string
		reject                     bool
	}{
		{"owned-option", "own value: Option[Window]", "i32", `let current = match (value) { Some(w) => w, None => Window { bytes: [], ready: false } }; return consume(current);`, false},
		{"fresh-local-option", "", "i32", `let value = Some(Window { bytes: [1u8], ready: false }); let current = match (value) { Some(w) => w, None => Window { bytes: [], ready: false } }; return consume(current);`, false},
		{"destructured-sibling", "own cursor: Cursor", "Result[(Cursor, i32), string]", `let Cursor { index, entry, target, window } = cursor; let current = match (window) { Some(value) => value, None => make(index.entries[entry], index.limit)? }; if (current.ready) { return take(index, entry, target, current); } let next = current.next()?; return Ok((Cursor { index: index, entry: entry, target: target, window: Some(next) }, 0));`, false},
		{"owned-alias", "own value: Option[Window]", "i32", `let other = value; let current = match (other) { Some(w) => w, None => Window { bytes: [], ready: false } }; return consume(current);`, false},
		{"borrowed-option", "value: Option[Window]", "i32", `let current = match (value) { Some(w) => w, None => Window { bytes: [], ready: false } }; return consume(current);`, true},
		{"source-read-later", "own value: Option[Window]", "i32", `let current = match (value) { Some(w) => w, None => Window { bytes: [], ready: false } }; let n = match (value) { Some(w) => w.bytes.len(), None => 0 }; return consume(current) + n;`, true},
		{"whole-read-later", "own cursor: Cursor", "i32", `let Cursor { index, entry, target, window } = cursor; let current = match (window) { Some(w) => w, None => Window { bytes: [], ready: false } }; let n = cursor.index.limit; return consume(current) + n;`, true},
		{"borrowed-yield", "own value: Option[Window], other: Window", "i32", `let current = match (value) { Some(w) => w, None => other }; return consume(current);`, true},
		{"borrowed-reassignment", "own value: Option[Window], other: Window", "i32", `let current = match (value) { Some(w) => w, None => Window { bytes: [], ready: false } }; current = other; return consume(current);`, true},
		{"payload-reassigned", "own value: Option[Window], other: Window", "i32", `let current = match (value) { Some(w) => { w = other; w }, None => Window { bytes: [], ready: false } }; return consume(current);`, true},
		{"captured-source", "own value: Option[Window]", "i32", `let later = () => match (value) { Some(w) => w.bytes.len(), None => 0 }; let current = match (value) { Some(w) => w, None => Window { bytes: [], ready: false } }; return consume(current) + later();`, true},
		{"guarded-fallback", "own value: Option[Window], flag: boolean", "i32", `let current = match (value) { Some(w) when flag => w, Some(w) => w, None => Window { bytes: [], ready: false } }; return consume(current);`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := ownedLocalMatchDecls + "function work(" + tc.params + "): " + tc.result + " { " + tc.body + " } function main(): i32 { return 0; }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if !tc.reject && len(ds) == 0 {
					return
				}
				if tc.reject {
					for _, d := range ds {
						if d.code == "E050" || d.code == "E051" {
							return
						}
					}
				}
				t.Fatalf("%s: reject=%v, diagnostics=%v", label, tc.reject, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostOwnedLocalMatchX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_local_match_checker")
	ownedLocalMatchCases(t, bin, runner, dir)
}

func TestSelfHostOwnedLocalMatchWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_local_match_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedLocalMatchCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostOwnedLocalMatchRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := ownedLocalMatchDecls + `
function run(own bytes: u8[], flag: boolean): Result[i32, string] {
 let cursor = Cursor { index: Index { entries: [0i64], limit: 1 }, entry: 0, target: 0i64, window: if (flag) { Some(Window { bytes: bytes, ready: flag }) } else { None } };
 let Cursor { index, entry, target, window } = cursor;
 let current = match (window) { Some(value) => value, None => make(index.entries[entry], index.limit)? };
 if (current.ready) { let (_, n) = take(index, entry, target, current)?; return Ok(n); }
 let next = current.next()?;
 let (_, n) = take(index, entry, target, next)?;
 return Ok(n);
}
function main(): i32 {
 let Ok(a) = run([args().len() as u8], true) else { return 1; };
 let Ok(b) = run([], false) else { return 2; };
 return if (a == 1 && b == 1) { 0 } else { 3 };
}`
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var command *exec.Cmd
			if target == "wasm32-wasi" {
				command = exec.Command(e2eharness.Wasmtime(t), "run", cli.wasmComponent(t, path, "FERN_STRICT_IR=1"))
			} else {
				command = runX86_64Bin(cli.runner, cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1"))
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", target, err, output)
			}
		})
	}
}
