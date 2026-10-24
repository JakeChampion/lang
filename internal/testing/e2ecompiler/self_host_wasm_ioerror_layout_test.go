package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostWasmIoErrorVariantLayout(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IoError layout e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	wasmRun := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	if err := os.WriteFile(filepath.Join(dir, "reg.txt"), []byte("the file"), 0o644); err != nil {
		t.Fatalf("write reg.txt: %v", err)
	}

	prog := func(path string) string {
		return `
function main(): i32 {
    match (read_file("` + path + `")) {
        Ok(s) => { write("OK:"); write(s); return 0; },
        Err(e) => {
            match (e) {
                NotFound(p) => { write("notfound:"); write(p); return 0; },
                PermissionDenied(p) => { write("denied:"); write(p); return 0; },
                AlreadyExists(p) => { write("exists"); return 1; },
                InvalidUtf8(p) => { write("utf8"); return 1; },
                Interrupted => { write("intr"); return 1; },
                Unsupported => { write("unsup"); return 1; },
                Other(p, m, n) => { write("other:"); write(p); write("/msg="); write(m); if (n == 20) { write("/errno=ENOTDIR"); } write(":end"); return 0; }
            }
        }
    }
    return 2;
}
`
	}

	storeAt := func(off int) string {
		return fmt.Sprintf("(i32.store (i32.add (local.get $r) (i32.const %d)) (local.get $path))", off)
	}

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"notfound", "nope.txt", "notfound:nope.txt"},
		{"other", "reg.txt/nested", "other:reg.txt/nested/msg=Not a directory/errno=ENOTDIR:end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, wasmRun, []byte(prog(tc.path)))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			if !strings.Contains(string(wat), storeAt(8)) {
				t.Fatalf("emitted core does not carry the IR IoError layout %q", storeAt(8))
			}
			if strings.Contains(string(wat), storeAt(4)) {
				t.Fatalf("emitted core carries the retired AST emitter's IoError layout %q", storeAt(4))
			}

			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			out, _ := exec.Command(wasmtime, "run", "--dir", dir+"::/", watPath).Output()
			if string(out) != tc.want {
				t.Errorf("stdout = %q, want %q", string(out), tc.want)
			}
		})
	}
}

func TestSelfHostWasmIoErrorMessageIsRcBoxed(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	wasmRun := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")

	const prog = `function main(): i32 {
    match (read_file("nope.txt")) {
        Ok(s) => { return 0; },
        Err(e) => {
            match (e) {
                Other(p, m, _) => { return m.len(); },
                _ => { return 0; }
            }
        }
    }
}
`
	wat := string(runCapture(t, gcc, runner, wasmRun, []byte(prog)))
	body := ioErrorBuilderBody(t, wat)
	if strings.Contains(body, "(call $__fern_alloc (i32.const 4))") {
		t.Error("the Other arm still builds its empty message with a bare $__fern_alloc — no rc word at [e-8]")
	}
	if got := strings.Count(body, "(call $__fern_str_box "); got != 8 {
		t.Errorf("$__fern_build_io_error makes %d $__fern_str_box calls, want 8 (seven variant boxes + the Other arm's message)", got)
	}
	if got := strings.Count(body, "(call $__fern_alloc "); got != 0 {
		t.Errorf("$__fern_build_io_error still makes %d bare $__fern_alloc calls, want 0", got)
	}
}

func ioErrorBuilderBody(t *testing.T, wat string) string {
	t.Helper()
	start := strings.Index(wat, "(func $__fern_build_io_error ")
	if start < 0 {
		t.Fatal("emitted WAT has no $__fern_build_io_error — the case is vacuous")
	}
	rest := wat[start:]
	end := strings.Index(rest, "\n  (func ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
