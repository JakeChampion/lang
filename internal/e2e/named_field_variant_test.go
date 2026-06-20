package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Named-field enum variants: declared `Rect { w: i32, h: i32 }`, matched
// `Rect { h, w }` (any field order, bound by name), and rendered by a
// derived Display as `Rect { w: 3, h: 4 }`. Construction is positional in
// v1 (`Rect(3, 4)`). See docs/NAMED-FIELD-VARIANTS.md.
const namedFieldVariantSrc = `import "std/i32";
import "core/cmp";

@derive(cmp.Display)
enum Shape {
    Circle { r: i32 },
    Rect { w: i32, h: i32 },
    Unit
}

function area(s: Shape): i32 {
    match (s) {
        Circle { r } => { return 3 * r * r; },
        Rect { h, w } => { return w * h; },
        Unit => { return 0; },
    }
    return 0 - 1;
}

function main(): i32 {
    var rr: Shape = Rect(3, 4);
    var c: Shape = Circle(2);
    print(rr.to_string());                 // Rect { w: 3, h: 4 }
    print("a=" + area(rr).to_string());    // a=12
    print("a=" + area(c).to_string());     // a=12
    return 0;
}
`

func TestInterpNamedFieldVariant(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(namedFieldVariantSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "Rect { w: 3, h: 4 }") || strings.Count(got, "a=12") < 2 {
		t.Errorf("stdout missing named-field Display / match output; got: %q (stderr: %s)", got, errb.String())
	}
}

func TestX86_64NamedFieldVariant(t *testing.T) {
	out, code := compileAndRunX86_64(t, namedFieldVariantSrc)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "Rect { w: 3, h: 4 }") || strings.Count(out, "a=12") < 2 {
		t.Errorf("x86-64 output missing expected lines; got:\n%s", out)
	}
}

func TestArm64NamedFieldVariant(t *testing.T) {
	out, code := compileAndRunArm64(t, namedFieldVariantSrc)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "Rect { w: 3, h: 4 }") || strings.Count(out, "a=12") < 2 {
		t.Errorf("arm64 output missing expected lines; got:\n%s", out)
	}
}

func TestWASMNamedFieldVariant(t *testing.T) {
	got := runWasmCapturingStdout(t, namedFieldVariantSrc)
	if !strings.Contains(got, "Rect { w: 3, h: 4 }") || strings.Count(got, "a=12") < 2 {
		t.Errorf("wasm output missing expected lines; got:\n%s", got)
	}
}

// The @derive(Json) object shape for a named-field variant on the
// interpreter: `{"Rect":{"w":3,"h":4}}`.
func TestInterpNamedFieldVariantJson(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/json";
@derive(json.Json)
enum Shape { Circle { r: i32 }, Rect { w: i32, h: i32 } }
function main(): i32 {
    print(Rect(3, 4).to_json());
    print(Circle(5).to_json());
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, `{"Rect":{"w":3,"h":4}}`) || !strings.Contains(got, `{"Circle":{"r":5}}`) {
		t.Errorf("stdout missing named-field JSON object shape; got: %q", got)
	}
}
