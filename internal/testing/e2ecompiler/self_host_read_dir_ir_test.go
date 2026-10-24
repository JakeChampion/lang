package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostReadDirIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-readdir-ir")) {
        Ok(d) => {
            match (write_file(d + "/a.txt", "x")) { Err(_) => { return 1; }, Ok(_) => {}, }
            match (write_file(d + "/b.txt", "y")) { Err(_) => { return 2; }, Ok(_) => {}, }
            match (read_dir(d)) {
                Ok(names) => {
                    let n: i32 = names.len();
                    match (remove_dir_all(d)) { Err(_) => { return 3; }, Ok(_) => {}, }
                    if (n != 2) { return 4; }
                    return 0;
                },
                Err(_) => { return 5; },
            }
        },
        Err(_) => { return 6; },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "call __fn___fern_read_dir") {
		t.Fatal("read_dir did not reach the Fern IR runtime (no call __fn___fern_read_dir in asm)")
	}
	progBin := buildBin(t, gcc, dir, "readdir_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("read_dir IR program exited %d, want 0 (temp_dir+write+read_dir(==2)+remove)", code)
	}
}

func TestSelfHostReadDirIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_dir wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	rd := filepath.Join(dir, "rd_dir")
	if err := os.Mkdir(rd, 0o755); err != nil {
		t.Fatalf("mkdir rd_dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rd, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rd, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rd, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	const src = `function main(): i32 {
    match (read_dir("rd_missing")) {
        Ok(_) => { return 5; },
        Err(_) => {
            match (read_dir("rd_dir")) {
                Ok(names) => {
                    if (names.len() != 3) { return 1; }
                    let total: i32 = 0;
                    let i: i32 = 0;
                    while (i < names.len()) {
                        let nm: string = names[i];
                        if (nm.len() == 0) { return 2; }
                        total = total + nm.len();
                        i = i + 1;
                    }
                    if (total != 13) { return 3; }
                    return 0;
                },
                Err(_) => { return 4; },
            }
        },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_read_dir")) {
		t.Fatal("read_dir did not reach the wasm IR runtime path (no call $__fern_read_dir in WAT)")
	}
	watFile := filepath.Join(dir, "rd_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("read_dir wasm IR program exited %d, want 0 (3 entries totalling 13 bytes / missing -> Err)\n--- WAT ---\n%s", code, wat)
	}
}

func TestSelfHostReadDirAllIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const src = `function main(): i32 {
    match (temp_dir("fern-readdirall-ir")) {
        Ok(d) => {
            match (write_file(d + "/a.txt", "x")) { Err(_) => { return 1; }, Ok(_) => {}, }
            match (write_file(d + "/b.txt", "y")) { Err(_) => { return 2; }, Ok(_) => {}, }
            match (read_dir_all(d)) {
                Ok(all) => {
                    match (read_dir(d)) {
                        Ok(plain) => {
                            match (remove_dir_all(d)) { Err(_) => { return 3; }, Ok(_) => {}, }
                            if (plain.len() != 2) { return 4; }
                            if (all.len() != 4) { return 5; }
                            let k: i32 = 0;
                            let j: i32 = 0;
                            let dots: i32 = 0;
                            while (k < all.len()) {
                                if (all[k] == "." || all[k] == "..") {
                                    dots = dots + 1;
                                } else {
                                    if (j >= plain.len()) { return 6; }
                                    if (all[k] != plain[j]) { return 7; }
                                    j = j + 1;
                                }
                                k = k + 1;
                            }
                            if (dots != 2) { return 8; }
                            if (j != plain.len()) { return 9; }
                            return 0;
                        },
                        Err(_) => { return 10; },
                    }
                },
                Err(_) => { return 11; },
            }
        },
        Err(_) => { return 12; },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !strings.Contains(string(asm), "call __fn___fern_read_dir_all") {
		t.Fatal("read_dir_all did not reach the Fern IR runtime (no call __fn___fern_read_dir_all in asm)")
	}
	progBin := buildBin(t, gcc, dir, "readdirall_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("read_dir_all IR program exited %d, want 0 (see the exit codes in src)", code)
	}
}

func TestSelfHostReadDirAllIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host read_dir_all wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	rd := filepath.Join(dir, "rda_dir")
	if err := os.Mkdir(rd, 0o755); err != nil {
		t.Fatalf("mkdir rda_dir: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(rd, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(rd, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	const src = `function main(): i32 {
    match (read_dir_all("rda_missing")) {
        Ok(_) => { return 5; },
        Err(_) => {
            match (read_dir_all("rda_dir")) {
                Ok(names) => {
                    if (names.len() != 5) { return 1; }
                    let dots: i32 = 0;
                    let i: i32 = 0;
                    while (i < names.len()) {
                        if (names[i] == "." || names[i] == "..") { dots = dots + 1; }
                        i = i + 1;
                    }
                    if (dots != 2) { return 2; }
                    return 0;
                },
                Err(_) => { return 4; },
            }
        },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(wat, []byte("call $__fern_read_dir_all")) {
		t.Fatal("read_dir_all did not reach the wasm IR runtime path (no call $__fern_read_dir_all in WAT)")
	}
	watFile := filepath.Join(dir, "rda_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", "--dir=.::/", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", wat)
	}
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("read_dir_all wasm IR program exited %d, want 0 (5 entries, 2 of them dots / missing -> Err)\n--- WAT ---\n%s", code, wat)
	}
}
