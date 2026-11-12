package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ownedFrameDecls = `struct Buffer { bytes: u8[] }
struct State { buffer: Buffer, other: u8[] }
enum Failure { Bad }
function (own b: Buffer) advance(): Result[Buffer, Failure] { return Ok(b); }
function (own b: Buffer) with_view(view: [u8]): Result[Buffer, Failure] { return Ok(b); }
function (own b: Buffer) maybe(flag: boolean): Result[Buffer, Failure] { return if (flag) { Ok(b) } else { Err(Failure.Bad) }; }
function advance(own b: Buffer, view: [u8]): Result[Buffer, Failure] { return Ok(b); }
function make(): Result[State, Failure] { return Ok(State { buffer: Buffer { bytes: [7u8] }, other: [] }); }
function number(view: [u8], flag: boolean): Result[i32, Failure] { return if (flag) { Ok(view.len()) } else { Err(Failure.Bad) }; }
function take(own s: State): i32 { return s.buffer.bytes.len(); }
function both(own a: State, own b: State): i32 { return a.other.len() + b.other.len(); }
`

func ownedFrameReaderCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	cases := []struct {
		name, params, result, body string
		reject                     bool
	}{
		{"receiver-field-update", "own s: State", "Result[State, Failure]", `return Ok(State { ...s, buffer: s.buffer.advance()? });`, false},
		{"receiver-field-view", "own s: State", "Result[State, Failure]", `return Ok(State { ...s, buffer: s.buffer.with_view(s.other)? });`, false},
		{"local-field-function", "own s: State", "Result[State, Failure]", `let b = State { ...s, other: [1u8] }; let next = advance(b.buffer, b.other)?; return Ok(State { ...b, buffer: next });`, false},
		{"local-field-receiver", "own s: State", "Result[State, Failure]", `let b = State { ...s, other: [1u8] }; let next = b.buffer.with_view(b.other)?; return Ok(State { ...b, buffer: next });`, false},
		{"fresh-local-field", "", "Result[State, Failure]", `let b = State { buffer: Buffer { bytes: [] }, other: [] }; let next = b.buffer.advance()?; return Ok(State { ...b, buffer: next });`, false},
		{"borrowed-local-field", "s: State", "Result[State, Failure]", `let b = State { ...s, other: [1u8] }; let next = advance(b.buffer, b.other)?; return Ok(State { ...b, buffer: next });`, true},
		{"later-field-read", "own s: State", "Result[State, Failure]", `let b = State { ...s, other: [] }; let next = b.buffer.advance()?; let n = b.buffer.bytes.len(); return Ok(State { ...b, buffer: next });`, true},
		{"capture-field", "own s: State", "Result[State, Failure]", `let b = State { ...s, other: [] }; let view = () => b.buffer.bytes.len(); let next = b.buffer.advance()?; let out = State { ...b, buffer: next }; let n = view(); return Ok(out);`, true},
		{"defer-field", "own s: State", "Result[State, Failure]", `let b = State { ...s, other: [] }; defer { let n = b.buffer.bytes.len(); } let next = b.buffer.advance()?; return Ok(State { ...b, buffer: next });`, true},
		{"duplicate-receiver", "own s: State", "Result[State, Failure]", `return Ok(State { ...s, buffer: s.buffer.with_view(s.buffer.bytes)? });`, true},
		{"wrong-replacement", "own s: State", "Result[State, Failure]", `let b = s.buffer.advance()?; return Ok(State { ...s, other: b.bytes });`, true},
		{"value-match-return", "flag: boolean", "i32", `let s = match (make()) { Ok(value) => value, Err(_) => { return 1; } }; let n = match (number(s.other, flag)) { Ok(value) => value, Err(_) => { return take(s); } }; return s.other.len() + n;`, false},
		{"value-if-return", "flag: boolean", "i32", `let s = match (make()) { Ok(value) => value, Err(_) => { return 1; } }; let n = if (flag) { 0 } else { return take(s); }; return s.other.len() + n;`, false},
		{"value-match-yield", "flag: boolean", "i32", `let s = match (make()) { Ok(value) => value, Err(_) => { return 1; } }; let n = match (number(s.other, flag)) { Ok(value) => value, Err(_) => take(s) }; return s.other.len() + n;`, true},
		{"value-return-duplicate", "flag: boolean", "i32", `let s = match (make()) { Ok(value) => value, Err(_) => { return 1; } }; let n = match (number(s.other, flag)) { Ok(value) => value, Err(_) => { return both(s, s); } }; return s.other.len() + n;`, true},
		{"value-return-later-argument", "flag: boolean", "i32", `let s = match (make()) { Ok(value) => value, Err(_) => { return 1; } }; let n = match (number(s.other, flag)) { Ok(value) => value, Err(_) => { return take(s) + s.other.len(); } }; return s.other.len() + n;`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := ownedFrameDecls + "function work(" + tc.params + "): " + tc.result + " { " + tc.body + " } function main(): i32 { return 0; }"
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

func TestSelfHostOwnedFrameReaderX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_frame_checker")
	ownedFrameReaderCases(t, bin, runner, dir)
}

func TestSelfHostOwnedFrameReaderWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_frame_checker.wasm", e2eharness.TargetWasm32Wasi)
	ownedFrameReaderCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostOwnedFrameReaderRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := "import \"std/string\";\n" + ownedFrameDecls + `function work(flag: boolean, name: string): i32 {
  let s = State { buffer: Buffer { bytes: name.bytes() }, other: [] };
  let n = match (number(s.other, flag)) { Ok(value) => value, Err(_) => { return take(s); } };
  let next = s.buffer.advance();
  let buffer = match (next) { Ok(value) => value, Err(_) => { return 9; } };
  let done = State { ...s, buffer: buffer };
  return take(done) + n;
}
function rebuild(own s: State, flag: boolean): Result[State, Failure] {
  return Ok(State { ...s, buffer: s.buffer.maybe(flag)? });
}
function main(): i32 {
  let argv = args();
  let name = argv[0];
  let flag = argv.len() == 1;
  if (work(flag, name) + work(!flag, name) != 2 * name.len()) { return 1; }
  let Ok(good) = rebuild(State { buffer: Buffer { bytes: name.bytes() }, other: name.bytes() }, true) else { return 2; };
  if (good.buffer.bytes.len() != name.len() || good.other.len() != name.len()) { return 3; }
  return match (rebuild(State { buffer: Buffer { bytes: name.bytes() }, other: name.bytes() }, false)) { Err(_) => 0, Ok(_) => 4 };
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
