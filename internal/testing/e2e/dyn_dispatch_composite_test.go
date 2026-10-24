package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const dynDispatchHdr = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
`

const dynDispatchMatchArm = dynDispatchHdr + `function main(): i32 {
    let r: Result[i32, dyn Error] = Err(NotFound { what: "ab" } as dyn Error);
    return match (r) { Ok(v) => v, Err(e) => e.message().len() };
}`

const dynDispatchField = dynDispatchHdr + `struct Box { e: dyn Error }
function main(): i32 {
    let b: Box = Box { e: NotFound { what: "ab" } as dyn Error };
    return b.e.message().len();
}`

const dynDispatchFieldImplicit = dynDispatchHdr + `struct Box { e: dyn Error }
function main(): i32 {
    let b: Box = Box { e: NotFound { what: "ab" } };
    return b.e.message().len();
}`

const dynDispatchArrayElem = dynDispatchHdr + `function main(): i32 {
    let xs: dyn Error[] = [NotFound { what: "ab" } as dyn Error];
    return xs[0].message().len();
}`

const dynDispatchEnumPayloadImplicit = dynDispatchHdr + `enum Wrapped { Wrap(dyn Error), Bare }
function main(): i32 {
    let w: Wrapped = Wrap(NotFound { what: "ab" });
    return match (w) { Wrap(e) => e.message().len(), Bare => 0 };
}`

func dynDispatchCases() map[string]string {
	return map[string]string{
		"match-arm":             dynDispatchMatchArm,
		"field":                 dynDispatchField,
		"field-implicit":        dynDispatchFieldImplicit,
		"enum-payload-implicit": dynDispatchEnumPayloadImplicit,
		"array":                 dynDispatchArrayElem,
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
