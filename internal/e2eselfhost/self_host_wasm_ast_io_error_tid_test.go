package e2eselfhost

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmAstPathIoErrorTypeIds guards the ONE place the legacy AST wasm
// emitter reaches into wasm_ir: $__fern_build_io_error (#5331 step 3).
//
// Type ids used to be baked as immediates (`i32.const 3`). They are positional —
// struct_type_id is an index into the struct table and prim_type_id is
// `structs.len() + i` — so adding a single struct anywhere in the program shifts
// the ids of every later struct AND every primitive dyn-box. Step 3 routes each
// site through a per-type global (`global.get $__tid$NotFound`) that the link
// step defines, which is what a per-module object cache needs: unlike string
// offsets and funcref indices, an id cannot be made unit-relative, because a box
// constructed in one module is tested in another and both must agree.
//
// build_io_error_func is shared with the AST path, which emits no
// tid_globals_section of its own. Without the declaration added alongside that
// call, the emitted WAT names an undefined global and fails to PARSE — so
// `wasm-tools parse` below is the real gate here, not the run.
//
// Reaching that path takes deliberate work: read_file/write_file lower on the IR
// path now, so a plain file-I/O program never gets here. The whole-program
// eligibility check (asm_ir.eligible_core_known_main) bails above 512 functions
// while the per-module view deliberately does not, so a 520-function module is
// the cheap, deterministic way in. The test asserts the fallback actually
// happened rather than trusting it: a program that quietly stayed on the IR path
// would exercise nothing, and this test would pass while guarding nothing.
func TestSelfHostWasmAstPathIoErrorTypeIds(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping AST-path io-error type-id e2e")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH; skipping AST-path io-error type-id e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// 520 trivial functions puts the module over the 512-function whole-program
	// budget, forcing the AST emitter; the read_file match is what pulls in
	// $__fern_build_io_error and its variant type ids.
	var b strings.Builder
	for i := 0; i < 520; i++ {
		fmt.Fprintf(&b, "function pad%d(): i32 { return %d; }\n", i, i)
	}
	b.WriteString(`function main(): i32 {
    match (read_file("definitely-absent.txt")) {
        Ok(s) => { return 1; },
        Err(e) => {
            match (e) {
                NotFound(p) => { return 42; },
                _ => { return 2; },
            }
        },
    }
}
`)

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(b.String()))
	watBytes, err := cmd.Output()
	if err != nil || len(watBytes) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	wat := string(watBytes)

	// The AST path must actually have been taken, and it must have emitted the
	// io-error helper — otherwise nothing below is being guarded.
	if !strings.Contains(wat, "$__fern_build_io_error") {
		t.Fatal("emitted WAT has no $__fern_build_io_error — the io-error helper was not reached")
	}
	if strings.Contains(wat, "$__str_base") {
		t.Fatal("emitted WAT declares $__str_base — the module stayed on the IR path, so the AST-path helper was never exercised")
	}

	// The reference and its declaration must both be present. Checked
	// explicitly so a regression reports which half went missing, rather than
	// surfacing as an opaque wasm-tools parse error.
	if !strings.Contains(wat, "global.get $__tid$NotFound") {
		t.Error("io-error helper does not read its variant id from $__tid$NotFound")
	}
	if !strings.Contains(wat, "(global $__tid$NotFound") {
		t.Error("$__tid$NotFound is referenced but never declared — the AST path is missing tid_globals_section")
	}

	// The real gate: an undefined global is a parse error, not a runtime one.
	watPath := filepath.Join(dir, "ast_io_error.wat")
	if err := os.WriteFile(watPath, watBytes, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	corePath := filepath.Join(dir, "ast_io_error.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse: %v\n%s", err, out)
	}

	// And the id must be the RIGHT one, not merely a declared one: a global
	// wired to the wrong variant still parses and still runs, but takes the
	// wrong match arm. Only running it distinguishes those.
	runCmd := exec.Command(wasmtime, "run", "--dir", dir, corePath)
	runCmd.Dir = dir
	out, err := runCmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("wasmtime run: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	if code != 42 {
		t.Errorf("exit code = %d, want 42 (the NotFound arm)\n%s", code, out)
	}
}
