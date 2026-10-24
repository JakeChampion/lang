package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var nestedArrPayloadCases = []struct {
	name string
	src  string
	want int
}{
	{"u8-nested-ok-payload", `function mk(): Result[u8[][], i32] {
    let one: u8[] = [97 as u8, 98 as u8];
    let seeds: u8[][] = [];
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
    let seeds: i32[][] = [];
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
	{"string-nested-err-payload", `function mk(): Result[i32, string[][]] {
    let rows: string[][] = [];
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
	{"lambda-captures-nested-payload", `function mk(): Result[u8[][], i32] {
    let one: u8[] = [40 as u8, 42 as u8];
    let seeds: u8[][] = [];
    seeds = seeds.append(one);
    return Ok(seeds);
}
function main(): i32 {
    match (mk()) {
        Ok(seeds) => {
            let f: () => i32 = () => (seeds[0][1] as i32);
            return f();
        },
        Err(_) => { return 93; }
    }
    return 94;
}`, 42},
}

func TestSelfHostNestedArrPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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

func TestSelfHostNestedArrPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

func TestSelfHostNestedArrPayloadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-arr-payload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range nestedArrPayloadCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
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
