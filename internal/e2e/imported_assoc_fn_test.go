package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Cross-module associated-function conformance. A user program imports
// std/num and seeds a generic reducer with the imported `T.zero()`
// associated function (the additive identity from the `num.Zero` trait).
//
// This pins the modload fix that exempts associated-function impl members
// from the `<mod>__` module prefix (and rewrites their AssocType). Before
// the fix, `impl num.Zero for i32 { function zero(): Self { return 0; } }`
// failed to register across the import boundary — the checker hoists it to
// `__assoc_i32_zero` from AssocType + the BARE name, but modload had
// prefixed the name to `num__zero`, producing `__assoc_i32_num__zero`,
// which no conformance check or `T.zero()` call site resolved. The program
// then errored with "i32 does not implement num.Zero: missing method zero".
// Instance-method conformance (`Add.add`, which carries a `self` param)
// already worked; only the no-`self` associated-function path was broken.
//
// 40 + 2 = 42.
const importedAssocFnZeroSrc = `import "std/num" as num;
function (a: T[]) total[T: num.Num + num.Zero](): T {
    var s: T = T.zero();
    var i: i32 = 0;
    while (i < a.len()) { s = s.add(a[i]); i = i + 1; }
    return s;
}
function main(): i32 {
    var xs: i32[] = [40, 2];
    return xs.total();
}
`

func TestInterpImportedAssocFnZero(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(importedAssocFnZeroSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestArm64ImportedAssocFnZero(t *testing.T) {
	if out, code := compileAndRunArm64(t, importedAssocFnZeroSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMImportedAssocFnZero(t *testing.T) {
	if code := runWasm(t, importedAssocFnZeroSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
