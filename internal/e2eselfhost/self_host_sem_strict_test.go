package e2eselfhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// FERN_SEM_IR_STRICT=1 turns the silent fallback to the AST lowering into a
// failed compile: exit 3, after the refusals FERN_SEM_IR_REPORT would print.
// A module the typed path produces whole compiles as it would without the flag,
// and without the flag a refused one still compiles. A runtime helper the typed
// path refuses fails the compile the same way.
func TestSelfHostSemIRStrict(t *testing.T) {
	gcc, _ := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	compileFor := func(target, src string, env ...string) (int, string) {
		path := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(fernBin, "-target", target, path, stdlibRoot, "-o", filepath.Join(t.TempDir(), "prog"))
		cmd.Env = append(os.Environ(), env...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), stderr.String()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0, stderr.String()
	}
	compile := func(src string, env ...string) (int, string) {
		return compileFor("x86-64-linux", src, env...)
	}

	produced := "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(2, 3); }\n"
	if code, out := compile(produced, "FERN_SEM_IR_STRICT=1"); code != 0 {
		t.Fatalf("a module produced whole: exit %d under strict\n%s", code, out)
	}
	// A trait method implemented for `str` in an imported module: the call
	// names `string.shout`, which the contract is keyed by too (#10883).
	strTrait := t.TempDir()
	if err := os.WriteFile(filepath.Join(strTrait, "leaf.fern"), []byte("pub trait Shout { function shout(self: Self): i32; }\nimpl Shout for str { function shout(self: Self): i32 { return self.len() + 1; } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strTrait, "main.fern"), []byte("import \"./leaf\";\nfunction main(): i32 { var s: str = \"ab\"; return s.shout(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	strTraitBin := filepath.Join(strTrait, "prog")
	strTraitBuild := exec.Command(fernBin, "-target", "x86-64-linux", filepath.Join(strTrait, "main.fern"), stdlibRoot, "-o", strTraitBin)
	strTraitBuild.Env = append(os.Environ(), "FERN_SEM_IR_STRICT=1")
	if out, err := strTraitBuild.CombinedOutput(); err != nil {
		t.Fatalf("an imported trait method on str under strict: %v\n%s", err, out)
	}
	var strTraitExit *exec.ExitError
	if err := exec.Command(strTraitBin).Run(); !errors.As(err, &strTraitExit) || strTraitExit.ExitCode() != 3 {
		t.Fatalf("an imported trait method on str under strict: %v, want exit 3", err)
	}
	async := "async function compute(): i32 { return 7; }\nfunction main(): i32 { return compute(); }\n"
	if code, out := compile(async, "FERN_SEM_IR_STRICT=1"); code != 0 {
		t.Fatalf("an async function: exit %d under strict\n%s", code, out)
	}

	// Each target spells the clock, id and termios helpers' sources on its own
	// syscalls. termios_get calls the fs bundle's __fern_io_error.
	helpers := `function main(): i32 {
    var ids: u32 = geteuid() + getegid() + getuid() + getgid();
    var t: i64 = monotonic_ns() + now_unix_ms() + now_ns();
    if (t < 0 || ids == 4294967295 as u32) { return 1; }
    match (termios_get(99)) {
        Ok(words) => { return words.len(); },
        Err(e) => { return 0; }
    }
}
`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if code, out := compileFor(target, helpers, "FERN_SEM_IR_STRICT=1"); code != 0 {
			t.Errorf("%s: the runtime helpers: exit %d under strict\n%s", target, code, out)
		}
	}

	// A read of a view map value takes a fresh box rather than the column's
	// own, so it compiles under strict (#10701).
	viewRead := `import "core/map";
function main(): i32 {
    var b: string = "abcdefgh";
    var m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(b, 2, 6));
    match (m.get(1)) { Some(v) => { return v.len(); }, None => { return 0; } }
}
`
	viewSrc := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(viewSrc, []byte(viewRead), 0o644); err != nil {
		t.Fatal(err)
	}
	viewBin := filepath.Join(t.TempDir(), "prog")
	viewBuild := exec.Command(fernBin, "-target", "x86-64-linux", viewSrc, stdlibRoot, "-o", viewBin)
	viewBuild.Env = append(os.Environ(), "FERN_SEM_IR_STRICT=1")
	if out, err := viewBuild.CombinedOutput(); err != nil {
		t.Fatalf("a view map value read under strict: %v, want the typed lowering's compile\n%s", err, out)
	}
	var viewExit *exec.ExitError
	if err := exec.Command(viewBin).Run(); !errors.As(err, &viewExit) || viewExit.ExitCode() != 4 {
		t.Fatalf("a view map value read under strict: %v, want exit 4", err)
	}

	// A function value whose type no declaration spells (`fs[0]`) keys no
	// clone, so a generic bound only through a fn-typed parameter stays erased
	// and its typed instances build the one `Slot__0_T` (#10827).
	unbound := `struct Slot[T] { v: T }

pub function hold[T](f: () => T): i32 {
    var c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

function main(): i32 {
    var fs: (() => i32)[] = [(): i32 => 7];
    return hold(fs[0]) + hold((): string => "x");
}
`
	if code, out := compile(unbound, "FERN_SEM_IR_STRICT=1"); code != 3 || !strings.Contains(out, "FERN_SEM_IR: hold$i32: record field type") {
		t.Fatalf("a generic over a variable an unspelled function value carries: exit %d under strict, want 3 naming the refusal\n%s", code, out)
	}
	if code, out := compile(unbound, "FERN_SEM_IR_STRICT="); code != 0 {
		t.Fatalf("the same without strict: exit %d, want the AST lowering's compile\n%s", code, out)
	}

	// The same call from a top-level statement: the scan reads the module's
	// statements as well as its functions.
	script := `struct Slot[T] { v: T }
pub function hold[T](f: () => T): i32 { var c: Slot[T] = Slot[T] { v: f() }; return 1; }
var fs: (() => i32)[] = [(): i32 => 7];
return hold(fs[0]) + hold((): string => "x");
`
	if code, out := compile(script, "FERN_SEM_IR_STRICT=1"); code != 3 || !strings.Contains(out, "FERN_SEM_IR: hold$i32: record field type") {
		t.Fatalf("a top-level call to a generic over an unspelled function value: exit %d under strict, want 3 naming the refusal\n%s", code, out)
	}
	scriptSrc := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(scriptSrc, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptBin := filepath.Join(t.TempDir(), "prog")
	build := exec.Command(fernBin, "-target", "x86-64-linux", scriptSrc, stdlibRoot, "-o", scriptBin)
	build.Env = append(os.Environ(), "FERN_SEM_IR_STRICT=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the top-level call without strict: %v, want the AST lowering's compile\n%s", err, out)
	}
	var scriptExit *exec.ExitError
	if err := exec.Command(scriptBin).Run(); !errors.As(err, &scriptExit) || scriptExit.ExitCode() != 2 {
		t.Fatalf("the top-level call without strict: %v, want exit 2", err)
	}

	// A generic callee's result spells the callee's own variable, so it binds
	// nothing: no clone is keyed on `0_K`, and hold stays erased as above.
	genericPick := `struct Slot[T] { v: T }

pub function pick[K](x: K): () => K { return (): K => x; }

pub function hold[T](f: () => T): i32 {
    var c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

function main(): i32 { return hold(pick(1)) + hold((): string => "x"); }
`
	if code, out := compile(genericPick, "FERN_SEM_IR_STRICT=1"); code != 3 || !strings.Contains(out, "FERN_SEM_IR: hold$i32: record field type") || strings.Contains(out, "hold__0_K") {
		t.Fatalf("a generic callee's function result: exit %d under strict, want 3 refusing hold$i32 and no clone keyed on its variable\n%s", code, out)
	}

	// A value holding a view merged past its source is copied there
	// (ssasem.deep_copy). Two kinds have no copy and are refused by name
	// (ssasem.copy_refusal): a function value, whose environment no test can
	// find, and a type that holds itself, whose copy would recurse. A closure
	// capturing a bare view is refused where it is built; one reaching a view
	// through a captured record is built and holds that view, and returned
	// past its source it is refused through Func.envs.
	for _, c := range []struct{ name, src, why string }{
		{"closure-in-a-record-merged", `import "std/i32";
struct P { a: str }
struct Holder { f: () => i32, n: i32 }
function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function g(n: i32): i32 {
    var h: Holder = Holder { f: () => 0, n: 0 };
    if (n != 0) {
        var s: string = mk(n);
        var p: P = P { a: slice_unchecked(s, 1, 4) };
        h = Holder { f: () => p.a.len() * 10 + (p.a[0] as i32) - 97, n: n };
    }
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return h.f() * 10 + h.n;
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`, "FERN_SEM_IR: g: a value merged past its source has no copy: a function value (() => i32) has no shape to rebuild its environment by"},
		{"recursive-enum-merged", `import "std/i32";
enum L { Cons(str, L), Nil }
function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function two(s: string): L { return Cons(slice_unchecked(s, 0, 1), Cons(slice_unchecked(s, 1, 3), Nil)); }
function count(l: L): i32 { match (l) { Cons(h, t) => { return h.len() + count(t); }, Nil => { return 0; } } return 0; }
function g(n: i32): i32 {
    var l: L = Nil;
    if (n != 0) {
        var s: string = mk(n);
        l = two(s);
    }
    return count(l);
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`, "FERN_SEM_IR: g: a value merged past its source has no copy: L holds itself, so its copy would recurse"},
		{"closure-over-a-record-returned", `import "std/i32";
function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
struct P { a: str }
function viewer(n: i32): () => i32 {
    var s: string = mk(n);
    var p: P = P { a: slice_unchecked(s, 1, 4) };
    return () => p.a.len() * 10 + (p.a[0] as i32) - 97;
}
function main(): i32 {
    var f: () => i32 = viewer(3);
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    print(f().to_string());
    return 0;
}
`, "FERN_SEM_IR: viewer: view result escapes its source"},
		{"closure-captures-a-view", `import "std/i32";
function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function viewer(n: i32): () => i32 {
    var s: string = mk(n);
    var v: str = slice_unchecked(s, 1, 4);
    return () => v.len() * 10 + (v[0] as i32) - 97;
}
function main(): i32 {
    var f: () => i32 = viewer(3);
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    print(f().to_string());
    return 0;
}
`, "FERN_SEM_IR: viewer: closure capture type"},
	} {
		if code, out := compile(c.src, "FERN_SEM_IR_STRICT=1"); code != 3 || !strings.Contains(out, c.why) {
			t.Fatalf("%s: exit %d under strict, want 3 naming %q\n%s", c.name, code, c.why, out)
		}
	}

	// A stream handle's `fd` is the handle's own descriptor, read as the i32
	// through a parameter, a local and an enum payload binding alike (std/tcp
	// hands a file body's fd to tcp_sendfile). It was an unsupported
	// projection: a handle wears a record's nominal but has no schema.
	handleFd := `enum Tail { NoTail, FileTail(Reader, i64) }
function fd_of(t: Tail): i32 {
    match (t) {
        FileTail(r, left) => { return r.fd; },
        _ => { return 0 - 1; }
    }
    return 0 - 1;
}
function fd_direct(r: Reader): i32 { return r.fd; }
function main(): i32 {
    var w: Writer = stdout();
    var t: Tail = FileTail(stdin(), 1 as i64);
    return w.fd * 10 + fd_of(t) + fd_direct(stdin()) + 5;
}
`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if code, out := compileFor(target, handleFd, "FERN_SEM_IR_STRICT=1"); code != 0 {
			t.Errorf("%s: a handle's fd read: exit %d under strict\n%s", target, code, out)
		}
	}
}
