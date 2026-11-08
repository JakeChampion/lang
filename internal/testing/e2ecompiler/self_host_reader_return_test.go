package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func readerReturnCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct State { n: i32 }
function (own s: State) step_with(read: (i32) => Result[u8[], IoError]): Result[(State, i32), string] {
    match (read(0)) {
        Ok(_) => { return Ok((State { n: s.n + 1 }, 1)); },
        Err(_) => { return Err("read"); }
    }
}
function (own s: State) step(reader: Reader): Result[(State, i32), string] {
    return s.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
function event(own s: State, reader: Reader): Result[(State, i32), string] { return s.step(reader); }
function (s: State) lend(reader: Reader): Reader { return reader; }
function alias(s: State, reader: Reader): Reader { return s.lend(reader); }
`
	for _, tc := range []struct{ name, body, want string }{
		{"reader-callback-state-loop", `let state = State { n: 0 }; for i in 0..2 { let (next, value) = match (event(state, reader)) { Ok(v) => v, Err(_) => { return 1; } }; state = next; } return state.n;`, ""},
		{"actual-reader-alias", `let copied = alias(State { n: 0 }, reader); match (copied.close()) { Ok(_) => {}, Err(_) => {} } return 0;`, "E051"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := decls + "function work(reader: Reader): i32 { " + tc.body + " } function main(): i32 { return 0; }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.want == "" && len(ds) == 0 {
					return
				}
				for _, d := range ds {
					if tc.want != "" && d.code == tc.want {
						return
					}
				}
				t.Fatalf("%s: want %q, got %v", label, tc.want, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) != 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostReaderReturnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "reader_return_checker")
	readerReturnCases(t, bin, runner, dir)
}

func TestSelfHostReaderReturnWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "reader_return_checker.wasm", e2eharness.TargetWasm32Wasi)
	readerReturnCases(t, bin, []string{wasmtime, "run"}, dir)
}
