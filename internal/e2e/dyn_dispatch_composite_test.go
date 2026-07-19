package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Regression for #3213: dispatching a `dyn Trait` method on a value that
// is NOT a simple local/param — bound from a Result/enum match arm, read
// from a struct field, or read from an array element — used to segfault on
// the compiled backends because the trait's vtable was emitted empty
// (`dynTraitNamesUsed` under-scanned, missing `dyn` that appears only
// nested in a composite type). Each case calls `.message()` (→ "ab", len 2).
const dynDispatchHdr = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
`

// (a) receiver bound from a Result Err match arm
const dynDispatchMatchArm = dynDispatchHdr + `function main(): i32 {
    var r: Result[i32, dyn Error] = Err(NotFound { what: "ab" } as dyn Error);
    return match (r) { Ok(v) => v, Err(e) => e.message().len() };
}`

// (b) receiver read from a struct field
const dynDispatchField = dynDispatchHdr + `struct Box { e: dyn Error }
function main(): i32 {
    var b: Box = Box { e: NotFound { what: "ab" } as dyn Error };
    return b.e.message().len();
}`

// (b') same as (b) but the field value coerces IMPLICITLY (no `as dyn Error`):
// the struct-field position was missing from assignable()'s dyn boxing-site
// list, so this reported a spurious E043. Now the concrete boxes into the dyn
// field like every other position; dispatch still reads "ab" (len 2).
const dynDispatchFieldImplicit = dynDispatchHdr + `struct Box { e: dyn Error }
function main(): i32 {
    var b: Box = Box { e: NotFound { what: "ab" } };
    return b.e.message().len();
}`

// (c) receiver read from an array element
const dynDispatchArrayElem = dynDispatchHdr + `function main(): i32 {
    var xs: dyn Error[] = [NotFound { what: "ab" } as dyn Error];
    return xs[0].message().len();
}`

func dynDispatchCases() map[string]string {
	return map[string]string{
		"match-arm":      dynDispatchMatchArm,
		"field":          dynDispatchField,
		"field-implicit": dynDispatchFieldImplicit,
		"array":          dynDispatchArrayElem,
	}
}

func TestInterpDynDispatchFromComposite(t *testing.T) {
	bin := buildLangBinForInterp(t)
	for name, src := range dynDispatchCases() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "prog.fern")
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin, "-interp", p)
			var out, errb bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 2 {
				t.Errorf("exit = %d, want 2\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
			}
		})
	}
}

func TestX86_64DynDispatchFromComposite(t *testing.T) {
	for name, src := range dynDispatchCases() {
		t.Run(name, func(t *testing.T) {
			if out, code := compileAndRunX86_64(t, src); code != 2 {
				t.Errorf("exit = %d, want 2\n%s", code, out)
			}
		})
	}
}

func TestArm64DynDispatchFromComposite(t *testing.T) {
	for name, src := range dynDispatchCases() {
		t.Run(name, func(t *testing.T) {
			if out, code := compileAndRunArm64(t, src); code != 2 {
				t.Errorf("exit = %d, want 2\n%s", code, out)
			}
		})
	}
}

func TestWASMDynDispatchFromComposite(t *testing.T) {
	for name, src := range dynDispatchCases() {
		t.Run(name, func(t *testing.T) {
			if code := runWasm(t, src); code != 2 {
				t.Errorf("wasm exit = %d, want 2", code)
			}
		})
	}
}
