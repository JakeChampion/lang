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
// Worth pinning because the boxer writes those
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
		// is strerror's text for the errno (#8265).
		{"other", "reg.txt/nested", "other:reg.txt/nested/msg=Not a directory:end"},
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

// TestSelfHostWasmIoErrorMessageIsRcBoxed pins the fourth instance of the
// headerless-box shape this migration keeps turning up (the Reader leaves,
// subprocess, strbuf, and now this one).
//
// `$__fern_build_io_error`'s six mapped arms all build their box with
// `$__fern_str_box`, which writes rc=1 at base and returns base+8. The default
// `Other(path, "")` arm built its MESSAGE string with a bare `$__fern_alloc(4)`
// instead, so that string had no rc word at [e-8].
//
// It is not shielded the way a low address would be: `$__fern_alloc` returns a
// bump-heap address, so the `i32.lt_u ... heap_base` guard in `$__fern_arr_dec`
// and `$__fern_rc_dec` does not fire, and each reads the last word of the
// PRECEDING allocation instead — decrementing a neighbour's payload, or pushing
// the block onto a freelist class derived from garbage. Native wasmbin sidesteps
// it by storing a null message, and the register backends use a real empty-string
// literal; only this body allocated one bare.
//
// Reachable from any wasm program that does file I/O and hits an errno outside
// the mapped set (EISDIR, ENOSPC, EBADF, ...) and then binds or drops the
// message — which the `other` case above does.
func TestSelfHostWasmIoErrorMessageIsRcBoxed(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	const prog = `function main(): i32 {
    match (read_file("nope.txt")) {
        Ok(s) => { return 0; },
        Err(e) => {
            match (e) {
                Other(p, m) => { return m.len(); },
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
	// Every allocation inside the builder must go through the rc-headered
	// constructor. Counting rather than merely finding one keeps the assertion
	// from passing on the strength of the six arms that were already correct.
	if got := strings.Count(body, "(call $__fern_str_box "); got != 8 {
		t.Errorf("$__fern_build_io_error makes %d $__fern_str_box calls, want 8 (seven variant boxes + the Other arm's message)", got)
	}
	if got := strings.Count(body, "(call $__fern_alloc "); got != 0 {
		t.Errorf("$__fern_build_io_error still makes %d bare $__fern_alloc calls, want 0", got)
	}
}

// ioErrorBuilderBody slices out just the $__fern_build_io_error function, so the
// counts above cannot be met by neighbouring helpers.
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
