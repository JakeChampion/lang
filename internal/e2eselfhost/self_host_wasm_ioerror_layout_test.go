package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmIoErrorVariantLayout pins the IoError variant box that
// read_file's Err payload carries to the layout its CONSUMER reads: field i at
// 8*(1+i).
//
// $__fern_build_io_error used to be shared by BOTH wasm emitters, which laid a
// variant out at different slot widths (the legacy AST emitter at 4*(1+i)), and
// the boxer emitted the IR layout for both — so an AST-path `NotFound(p)` read
// its path out of the upper half of the id slot and printed garbage, and the
// two-field default arm `Other(path, msg)` was wrong in both fields (#5795).
// The AST emitter is gone (#3457) and its two cases with it; what remains is the
// IR layout itself, which is still worth pinning because the boxer writes those
// offsets by hand.
//
// Each case asserts WHICH layout the emitted core carries before running it, so
// a silent change to the box geometry reads as a diff rather than as garbage
// output.
func TestSelfHostWasmIoErrorVariantLayout(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IoError layout e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// A regular file, so that read_file("reg.txt/nested") fails with an errno
	// outside the mapped five (ENOTDIR) and lands on the two-field Other arm.
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
                Other(p, m) => { write("other:"); write(p); write("/msg="); write(m); write(":end"); return 0; }
            }
        }
    }
    return 2;
}
`
	}

	// The boxer's NotFound store — field 0 of a single-string variant — reads
	// back the emitted slot width.
	storeAt := func(off int) string {
		return fmt.Sprintf("(i32.store (i32.add (local.get $r) (i32.const %d)) (local.get $path))", off)
	}

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"notfound", "nope.txt", "notfound:nope.txt"},
		// Every unmapped errno lands on Other(path, msg): two fields, so it
		// pins the stride and not just the first field's offset. The message
		// is the empty string on wasm — the boxer builds Other(path, "").
		{"other", "reg.txt/nested", "other:reg.txt/nested/msg=:end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, wasmRun, []byte(prog(tc.path)))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			// Guard the case against becoming vacuous: confirm the box really
			// carries the IR consumer's geometry.
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
