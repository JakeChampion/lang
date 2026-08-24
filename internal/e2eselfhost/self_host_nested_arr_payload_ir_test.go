package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// nestedArrPayloadCases pin the NESTED-array (`T[][]`) Option/Result match
// payload on the IR path (the std/fuzz corpus shape: `match
// (fuzz_corpus_from_dir(d)) { Ok(seeds) => … }` over a `Result[u8[][], E]`).
// The payload classifier admitted only flat arrays, so any `T[][]` payload
// binding bailed the whole function (`did not lower: match`). The binding now
// rides the flat-array pointer read plus the is_arrarr + inner-element-kind
// marks a `var m: T[][]` binding records, so nested reads (`seeds[0][1]`),
// inner string dispatch (`rows[0][1].len()`), and a lambda CAPTURING the
// binding all resolve. Strict-IR drives each case so a bail fails the test
// rather than being absorbed by the module-level retry.
var nestedArrPayloadCases = []struct {
	name string
	src  string
	want int
}{
	{"u8-nested-ok-payload", `function mk(): Result[u8[][], i32] {
    var one: u8[] = [97 as u8, 98 as u8];
    var seeds: u8[][] = [];
    seeds = seeds.append(one);
    return Ok(seeds);
}
function main(): i32 {
    match (mk()) {
        Ok(loaded) => {
            if (loaded.len() != 1) { return 91; }
            if ((loaded[0][1] as i32) != 98) { return 92; }
            return 42;
        },
        Err(_) => { return 93; }
    }
    return 94;
}`, 42},
	{"i32-nested-ok-payload", `function mk(): Result[i32[][], i32] {
    var seeds: i32[][] = [];
    seeds = seeds.append([7, 8]);
    return Ok(seeds);
}
function main(): i32 {
    match (mk()) {
        Ok(loaded) => { if (loaded[0][1] != 8) { return 91; } return 42; },
        Err(_) => { return 93; }
    }
    return 94;
}`, 42},
	// Err-side nested payload with a string inner element: the binding's
	// arrarr_elem "string" is what routes `rows[0][1].len()` to str_len.
	{"string-nested-err-payload", `function mk(): Result[i32, string[][]] {
    var rows: string[][] = [];
    rows = rows.append(["ab", "cdef"]);
    return Err(rows);
}
function main(): i32 {
    match (mk()) {
        Ok(_) => { return 91; },
        Err(rows) => { return rows[0][1].len() * 10 + rows.len(); }
    }
    return 94;
}`, 41},
	// A lambda capturing the nested-array payload binding (the fuzz corpus
	// test's `() => test.assert_eq_array(seeds[0], …)` shape, minus stdlib).
	{"lambda-captures-nested-payload", `function mk(): Result[u8[][], i32] {
    var one: u8[] = [40 as u8, 42 as u8];
    var seeds: u8[][] = [];
    seeds = seeds.append(one);
    return Ok(seeds);
}
function main(): i32 {
    match (mk()) {
        Ok(seeds) => {
            var f: () => i32 = () => (seeds[0][1] as i32);
            return f();
        },
        Err(_) => { return 93; }
    }
    return 94;
}`, 42},
}

// TestSelfHostNestedArrPayloadIRX86_64 drives the cases through the
// self-hosted x86-64 compiler under FERN_STRICT_IR.
func TestSelfHostNestedArrPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range nestedArrPayloadCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (9x = wrong arm or wrong element value)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostNestedArrPayloadIRArm64 is the arm64 leg: the fix lives in the
// shared irlower.fern, so the leg differs only in which backend lowers it.
// Case table shared with the x86-64 leg; binaries run under qemu.
func TestSelfHostNestedArrPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedArrPayloadCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (9x = wrong arm or wrong element value)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostNestedArrPayloadWasmIR is the wasm leg, run under wasmtime.
// Skips without wasmtime on PATH.
func TestSelfHostNestedArrPayloadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-arr-payload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedArrPayloadCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			var stderr bytes.Buffer
			rcmd.Stderr = &stderr
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally\n%s", tc.name, stderr.String())
			}
			if code := rcmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d\n%s", tc.name, code, tc.want, stderr.String())
			}
		})
	}
}
