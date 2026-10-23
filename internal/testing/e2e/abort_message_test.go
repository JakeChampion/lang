package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// abortCase is a program that aborts (or, for the control, does not) with an
// expected exit code and stderr substring.
type abortCase struct {
	name     string
	src      string
	wantExit int
	wantErr  string // substring expected on stderr ("" = expect none)
}

var abortCases = []abortCase{
	{
		name:     "array_negative_index",
		src:      `function main(): i32 { let xs: i32[] = [10, 20, 30]; return xs[0 - 1]; }`,
		wantExit: 134,
		wantErr:  "array index out of range",
	},
	{
		name:     "array_update_oob",
		src:      `function main(): i32 { let xs: i32[] = [10, 20, 30]; return xs.with(9, 1).len(); }`,
		wantExit: 134,
		wantErr:  "array index out of range",
	},
	{
		name:     "string_index_oob",
		src:      `function main(): i32 { let s = "hi"; return s[9] as i32; }`,
		wantExit: 134,
		wantErr:  "string index out of range",
	},
	{
		name:     "negative_allocation",
		src:      `@noinline function alloc(n: i32): i32 { let b: u8[] = __alloc_u8(n); return b.len(); } function main(): i32 { return alloc(0 - 1); }`,
		wantExit: 134,
		wantErr:  "allocation size out of range",
	},
	{
		name:     "array_oob",
		src:      `function main(): i32 { let xs: i32[] = [10, 20, 30]; return xs[7]; }`,
		wantExit: 134,
		wantErr:  "array index out of range",
	},
	{
		// The trap lives in the UNCHECKED producer since #5634: the
		// checked `s[1:9]` answers None instead of aborting, so
		// `slice_unchecked` is the only route left to this message.
		name:     "string_slice_oob",
		src:      `function main(): i32 { let s: string = "hi"; let t: str = slice_unchecked(s, 1, 9); return t.len(); }`,
		wantExit: 134,
		wantErr:  "string index out of range",
	},
	{
		name:     "slice_range_oob",
		src:      `function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys: [i32] = xs[1:9]; return ys.len(); }`,
		wantExit: 134,
		wantErr:  "slice range out of bounds",
	},
	{
		name:     "in_bounds_ok",
		src:      `function main(): i32 { let xs: i32[] = [10, 20, 30]; if (xs[1] != 20) { return 1; } return 0; }`,
		wantExit: 0,
		wantErr:  "",
	},
}

// runAbortCases builds each case for `target` under `backend` ("" for the
// target's default) and runs it via `run`, asserting the exit code and that
// the abort's cause is named on stderr (or, for the in-bounds control, that
// stderr stays clean). Every native emitter must produce the SAME diagnostic
// (#5538) — the shared case table pins that.
func runAbortCases(t *testing.T, target, backend string, run func(bin string) *exec.Cmd) {
	t.Helper()
	bin := buildFernCLI(t)
	dir := t.TempDir()
	for _, tc := range abortCases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, target+backend+"_"+tc.name+".fern")
			if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			out := filepath.Join(dir, target+"_"+tc.name+".bin")
			args := []string{"-target", target, "-o", out, p}
			if backend != "" {
				args = append([]string{"-backend", backend}, args...)
			}
			if o, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, o)
			}
			cmd := run(out)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.wantExit {
				t.Errorf("exit = %d, want %d", code, tc.wantExit)
			}
			errOut := stderr.String()
			if tc.wantErr == "" {
				if errOut != "" {
					t.Errorf("stderr = %q, want empty", errOut)
				}
				return
			}
			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want to contain %q", errOut, tc.wantErr)
			}
			if !strings.HasPrefix(errOut, "fern: ") {
				t.Errorf("stderr = %q, want a 'fern: ' diagnostic prefix", errOut)
			}
		})
	}
}

// TestX86_64AbortMessages: a fatal abort (#5538) writes a diagnostic naming its
// cause to stderr before exiting, instead of exiting with a bare code.
func TestX86_64AbortMessages(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	runAbortCases(t, "x86-64-linux", "", func(bin string) *exec.Cmd { return runX86Bin(qemu, bin) })
}

// TestArm64AbortMessages is the arm64 parity check: the same programs abort
// with the same diagnostics as x86-64 (identical text — pinned by the shared
// abortCases table).
func TestArm64AbortMessages(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	runAbortCases(t, "arm64-linux", "", func(bin string) *exec.Cmd {
		if qemu == "" {
			return exec.Command(bin)
		}
		return exec.Command(qemu, bin)
	})
}

func TestWasmAbortMessages(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Fatal(err)
	}
	runAbortCases(t, "wasm32-wasi", "", func(bin string) *exec.Cmd {
		return exec.Command(wasmtime, "run", bin)
	})
}

func TestWasmAbortOutOfMemory(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "oom.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 { let b: u8[] = __alloc_u8(16777216); return b.len(); }`), 0o644); err != nil {
		t.Fatal(err)
	}
	wasm := filepath.Join(dir, "oom.wasm")
	if out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", wasm, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	cmd := exec.Command("wasmtime", "run", "-W", "max-memory-size=8388608", wasm)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
		t.Fatalf("want trap status 134:\n%s", out)
	}
	if !strings.HasPrefix(string(out), "fern: out of memory (heap arena exhausted)\n") {
		t.Fatalf("out-of-memory abort must report without another allocation:\n%s", out)
	}
}
