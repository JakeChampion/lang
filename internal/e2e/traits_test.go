package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: a `trait` + `impl Trait for Type` makes the impl methods
// callable through the ordinary method-dispatch path on the
// interpreter. This is the Phase 1 contract from docs/TRAITS.md —
// impl methods are desugared to receiver-methods, so no codegen / IR /
// interp change is needed; `p.to_string()` just resolves to
// `__method_Point_to_string`.
func TestInterpTraitImplDispatch(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/i32";

trait Display {
    function to_string(self: Self): string;
}

struct Point { x: i32, y: i32 }

impl Display for Point {
    function to_string(self: Self): string {
        return "(" + self.x.to_string() + ", " + self.y.to_string() + ")";
    }
}

function main(): i32 {
    var p: Point = Point { x: 3, y: 7 };
    print(p.to_string());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "(3, 7)") {
		t.Errorf("stdout missing trait-dispatched output `(3, 7)`: %q (stderr: %s)", out.String(), errb.String())
	}
}

// A type may implement more than one trait, and a trait may be
// implemented for several types; each impl's methods dispatch
// independently. Exercises a built-in-type impl (i32) alongside a
// struct impl.
func TestInterpTraitMultipleImpls(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/i32";

trait Named {
    function name(self: Self): string;
}

struct Dog { age: i32 }

impl Named for Dog {
    function name(self: Self): string { return "dog"; }
}

impl Named for i32 {
    function name(self: Self): string { return "int"; }
}

function main(): i32 {
    var d: Dog = Dog { age: 2 };
    var n: i32 = 5;
    print(d.name());
    print(n.name());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "dog") || !strings.Contains(got, "int") {
		t.Errorf("stdout missing both impl outputs (`dog`, `int`): %q (stderr: %s)", got, errb.String())
	}
}

// End-to-end: a trait-bounded generic function `show[T: Display](v: T)`
// dispatches `v.to_string()` to the right impl per instantiation. The
// monomorphiser clones `show` per concrete type and the re-check
// resolves each call to the impl's mangled method. An empty
// `impl Display for i32 {}` adopts i32's pre-existing std/i32
// to_string. See docs/TRAITS.md (Phase 2).
func TestInterpBoundedGenericDispatch(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/i32";

trait Display {
    function to_string(self: Self): string;
}

struct Point { x: i32, y: i32 }

impl Display for Point {
    function to_string(self: Self): string {
        return "Point(" + self.x.to_string() + ")";
    }
}

// Empty impl adopts i32's existing to_string from std/i32.
impl Display for i32 { }

function show[T: Display](v: T): string {
    return v.to_string();
}

function main(): i32 {
    var p: Point = Point { x: 9, y: 2 };
    print(show(p));
    print(show(42));
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "Point(9)") || !strings.Contains(got, "42") {
		t.Errorf("stdout missing per-instantiation dispatch output (`Point(9)`, `42`): %q (stderr: %s)", got, errb.String())
	}
}
