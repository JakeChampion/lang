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
// read_file's Err payload carries to the layout its CONSUMER reads.
//
// $__fern_build_io_error is shared by both wasm emitters, but they lay a
// variant out at different slot widths: the IR consumer puts field i at
// 8*(1+i), the legacy AST emitter at 4*(1+i) (wasm.struct_field_off). The
// boxer emitted the IR layout for both, so an AST-path `NotFound(p)` read its
// path out of the upper half of the id slot and printed garbage, and the
// two-field default arm `Other(path, msg)` — where every errno outside the
// mapped five lands — was wrong in both fields (#5795).
//
// Each case asserts WHICH layout the emitted core carries before running it.
// Without that, a change to IR eligibility would quietly route the ast case
// through the IR path, and the case would keep passing while covering nothing.
func TestSelfHostWasmIoErrorVariantLayout(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IoError layout e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// A regular file, so that read_file("reg.txt/nested") fails with an errno
	// outside the mapped five (ENOTDIR) and lands on the two-field Other arm.
	if err := os.WriteFile(filepath.Join(dir, "reg.txt"), []byte("the file"), 0o644); err != nil {
		t.Fatalf("write reg.txt: %v", err)
	}

	// The IR path takes any module inside its subset, so the AST case has to
	// push the module out of it. eligible_core caps IR routing at 512
	// functions; padding past that is the cheapest lever that leaves the
	// program itself entirely ordinary.
	var pad strings.Builder
	for i := 0; i < 520; i++ {
		fmt.Fprintf(&pad, "function fn%d(x: i32): i32 { return x + %d; }\n", i, i)
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
		ast  bool // pad the module out of the IR subset
		path string
		want string
	}{
		{"ir-notfound", false, "nope.txt", "notfound:nope.txt"},
		{"ast-notfound", true, "nope.txt", "notfound:nope.txt"},
		// Every unmapped errno lands on Other(path, msg): two fields, so it
		// pins the stride and not just the first field's offset. The message
		// is the empty string on wasm — the boxer builds Other(path, "").
		{"ir-other", false, "reg.txt/nested", "other:reg.txt/nested/msg=:end"},
		{"ast-other", true, "reg.txt/nested", "other:reg.txt/nested/msg=:end"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := prog(tc.path)
			if tc.ast {
				src = pad.String() + src
			}
			wat := runCapture(t, gcc, runner, wasmRun, []byte(src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			// Guard the case against becoming vacuous: confirm it really is on
			// the emitter it means to cover.
			want, other := storeAt(8), storeAt(4)
			if tc.ast {
				want, other = other, want
			}
			if !strings.Contains(string(wat), want) {
				t.Fatalf("emitted core does not carry the expected IoError layout %q — this case is no longer covering the path it names", want)
			}
			if strings.Contains(string(wat), other) {
				t.Fatalf("emitted core carries the other consumer's IoError layout %q", other)
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
