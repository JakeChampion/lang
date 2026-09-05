// Package e2eharness holds the shared e2e test harness — driver builds,
// tooling discovery, caches — used by both internal/e2e and
// internal/e2eselfhost (#4398 part 3). Extracted verbatim from
// internal/e2e/diff_oracle_test.go.
package e2eharness

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// CompileAndRunWasmbinMain runs the in-process parse → check →
// wasmbin.Build pipeline on src, writes the core wasm bytes to
// disk, and invokes `wasmtime run --invoke main` to call main()
// directly. The exit status of wasmtime --invoke is the i32 the
// callee returned, printed as a decimal integer on stdout.
// Returned value is masked to a byte to match the interpreter's
// `result & 0xFF` shape used elsewhere in this file.
//
// Skips the test if wasmtime is not on PATH. The wasmbin path
// does not depend on the preview-2 toolchain — it emits a raw
// core module that wasmtime runs without component-wrapping.
func CompileAndRunWasmbinMain(t *testing.T, src string) int {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v\nsrc:\n%s", err, src)
	}
	if err := constfold.FoldWith(prog, constfold.Inputs{TargetOS: "wasi"}); err != nil {
		t.Fatalf("constfold: %v\nsrc:\n%s", err, src)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v\nsrc:\n%s", err, src)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v\nsrc:\n%s", err, src)
	}
	bin, err := wasmbin.Build(prog, info)
	if err != nil {
		// Build will surface acknowledged gaps in wasmbin coverage
		// as "unsupported" or "unsupported op" / "unsupported type"
		// errors. Skip those: they're tracking signal, not
		// miscompilation bugs. Any other error (parser, checker,
		// IR pipeline) is unexpected and should fail.
		msg := err.Error()
		// Build-time coverage gaps. Categories:
		//   - "unsupported" — explicit "we don't handle X yet"
		//     errors from valtypeFor / op-emit / blocktype.
		//   - "unknown callee" — OpCallDirect targets the IR
		//     emits for builtins that haven't been wired through
		//     callDirectAlias or runtime helpers yet (e.g.
		//     `string_from_bytes_unchecked`).
		// Both are tracking signal, not miscompilation bugs.
		if strings.Contains(msg, "unsupported") ||
			strings.Contains(msg, "unknown callee") {
			t.Skipf("wasmbin coverage gap: %v", err)
		}
		t.Fatalf("wasmbin.Build: %v\nsrc:\n%s", err, src)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		// A module the validator rejects is always a bug, never a
		// coverage gap. A gap is declared at Build time (above):
		// the emitter knows it cannot lower a construct and says
		// so. Reaching here means Build claimed success and
		// produced bytes that are not a valid module — and the
		// validator's "type mismatch at offset N" reads identically
		// whether wasmbin emitted the wrong opcode or the front end
		// handed it wrongly-typed IR. Since the two are
		// indistinguishable from the error text, both fail: wasm's
		// validator is the only checker in the project that reports
		// the "lowering defaulted to i32" class at all, so skipping
		// on it hides front-end miscompiles from every inline-source
		// e2e test that runs this helper (#8456).
		if strings.Contains(se.String(), "type mismatch") ||
			strings.Contains(se.String(), "WebAssembly translation error") ||
			strings.Contains(se.String(), "Invalid input WebAssembly code") {
			t.Fatalf("wasmbin emitted an invalid module — a front-end "+
				"miscompile or a wasmbin emit bug, not a coverage gap. "+
				"If wasmbin genuinely cannot lower this construct, make "+
				"wasmbin.Build return an \"unsupported\" error for it so "+
				"the gap is declared at emit time and this helper skips.\n"+
				"%v\nstderr:\n%s\nsrc:\n%s", err, se.String(), src)
		}
		t.Fatalf("wasmtime: %v\nstderr:\n%s\nsrc:\n%s", err, se.String(), src)
	}
	trimmed := strings.TrimSpace(so.String())
	got, err := strconv.Atoi(trimmed)
	if err != nil {
		t.Fatalf("parse wasmbin stdout %q: %v\nsrc:\n%s", trimmed, err, src)
	}
	return got & 0xFF
}
