package e2eselfhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A module the typed lowering refuses fails the compile: exit 3, after a
// `FERN_SEM_IR:` line naming each refusal. Every compile here runs with no
// FERN_ variable of the caller's (childEnv).
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
		cmd.Env = childEnv(env...)
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
	if code, out := compile(produced); code != 0 {
		t.Fatalf("a module produced whole: exit %d\n%s", code, out)
	}
	// A trait method implemented for `str` in an imported module: the call
	// names `string.shout`, which the contract is keyed by too (#10883).
	strTrait := t.TempDir()
	if err := os.WriteFile(filepath.Join(strTrait, "leaf.fern"), []byte("pub trait Shout { function shout(self: Self): i32; }\nimpl Shout for str { function shout(self: Self): i32 { return self.len() + 1; } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strTrait, "main.fern"), []byte("import \"./leaf\";\nfunction main(): i32 { let s: str = \"ab\"; return s.shout(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	strTraitBin := filepath.Join(strTrait, "prog")
	strTraitBuild := exec.Command(fernBin, "-target", "x86-64-linux", filepath.Join(strTrait, "main.fern"), stdlibRoot, "-o", strTraitBin)
	strTraitBuild.Env = childEnv()
	if out, err := strTraitBuild.CombinedOutput(); err != nil {
		t.Fatalf("an imported trait method on str: %v\n%s", err, out)
	}
	var strTraitExit *exec.ExitError
	if err := exec.Command(strTraitBin).Run(); !errors.As(err, &strTraitExit) || strTraitExit.ExitCode() != 3 {
		t.Fatalf("an imported trait method on str: %v, want exit 3", err)
	}
	async := "async function compute(): i32 { return 7; }\nfunction main(): i32 { return compute(); }\n"
	if code, out := compile(async); code != 0 {
		t.Fatalf("an async function: exit %d\n%s", code, out)
	}

	// Each target spells the clock, id and termios helpers' sources on its own
	// syscalls. termios_get calls the fs bundle's __fern_io_error.
	helpers := `function main(): i32 {
    let ids: u32 = geteuid() + getegid() + getuid() + getgid();
    let t: i64 = monotonic_ns() + now_unix_ms() + now_ns();
    if (t < 0 || ids == 4294967295 as u32) { return 1; }
    match (termios_get(99)) {
        Ok(words) => { return words.len(); },
        Err(e) => { return 0; }
    }
}
`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if code, out := compileFor(target, helpers); code != 0 {
			t.Errorf("%s: the runtime helpers: exit %d\n%s", target, code, out)
		}
	}

	// A read of a view map value takes a fresh box rather than the column's
	// own, so the typed lowering takes it (#10701).
	viewRead := `import "core/map";
function main(): i32 {
    let b: string = "abcdefgh";
    let m: Map[i32, str] = map_new(4);
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
	viewBuild.Env = childEnv()
	if out, err := viewBuild.CombinedOutput(); err != nil {
		t.Fatalf("a view map value read: %v, want the typed lowering's compile\n%s", err, out)
	}
	var viewExit *exec.ExitError
	if err := exec.Command(viewBin).Run(); !errors.As(err, &viewExit) || viewExit.ExitCode() != 4 {
		t.Fatalf("a view map value read: %v, want exit 4", err)
	}

	// A function value whose type no declaration spells (`fs[0]`) keys no
	// clone, so a generic bound only through a fn-typed parameter stays
	// erased, and each typed instance builds `Slot__0_T` at its own binding
	// (#10827). An erased generic forwarding its function parameter calls the
	// clone keyed at its own variable, `hold__0_U`, a template the same way.
	run := func(src string, want int) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(t.TempDir(), "prog")
		build := exec.Command(fernBin, "-target", "x86-64-linux", path, stdlibRoot, "-o", bin)
		build.Env = childEnv()
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, out)
		}
		var exit *exec.ExitError
		if err := exec.Command(bin).Run(); !errors.As(err, &exit) || exit.ExitCode() != want {
			t.Fatalf("run: %v, want exit %d", err, want)
		}
	}
	run(`struct Slot[T] { v: T }

pub function hold[T](f: () => T): i32 {
    let c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

function main(): i32 {
    let fs: (() => i32)[] = [(): i32 => 7];
    return hold(fs[0]) + hold((): string => "x");
}
`, 2)
	run(`struct Slot[T] { v: T }

pub function hold[T](f: () => T): i32 {
    let c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

pub function wrap[U](g: () => U): i32 { return hold(g); }

function main(): i32 { return wrap((): i32 => 7) + wrap((): string => "x"); }
`, 2)

	// The same call from a top-level statement: the scan reads the module's
	// statements as well as its functions.
	run(`struct Slot[T] { v: T }
pub function hold[T](f: () => T): i32 { let c: Slot[T] = Slot[T] { v: f() }; return 1; }
let fs: (() => i32)[] = [(): i32 => 7];
return hold(fs[0]) + hold((): string => "x");
`, 2)

	// A generic callee's result spells the callee's own variable, so it binds
	// nothing: no clone is keyed on `0_K`, and hold stays erased as above.
	genericPick := `struct Slot[T] { v: T }

pub function pick[K](x: K): () => K { return (): K => x; }

pub function hold[T](f: () => T): i32 {
    let c: Slot[T] = Slot[T] { v: f() };
    return 1;
}

function main(): i32 { return hold(pick(1)) + hold((): string => "x"); }
`
	run(genericPick, 2)
	pickSrc := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(pickSrc, []byte(genericPick), 0o644); err != nil {
		t.Fatal(err)
	}
	pickAsm := filepath.Join(t.TempDir(), "prog.s")
	emit := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", pickSrc, stdlibRoot, "-o", pickAsm)
	emit.Env = childEnv()
	if out, err := emit.CombinedOutput(); err != nil {
		t.Fatalf("a generic callee's function result: %v\n%s", err, out)
	}
	if asm, err := os.ReadFile(pickAsm); err != nil || strings.Contains(string(asm), "hold__0_K") {
		t.Fatalf("a generic callee's function result: %v, want no clone keyed on its variable", err)
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
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function g(n: i32): i32 {
    let h: Holder = Holder { f: () => 0, n: 0 };
    if (n != 0) {
        let s: string = mk(n);
        let p: P = P { a: slice_unchecked(s, 1, 4) };
        h = Holder { f: () => p.a.len() * 10 + (p.a[0] as i32) - 97, n: n };
    }
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return h.f() * 10 + h.n;
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`, "FERN_SEM_IR: g: a value merged past its source has no copy: a function value (() => i32) has no shape to rebuild its environment by"},
		{"recursive-enum-merged", `import "std/i32";
enum L { Cons(str, L), Nil }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function two(s: string): L { return Cons(slice_unchecked(s, 0, 1), Cons(slice_unchecked(s, 1, 3), Nil)); }
function count(l: L): i32 { match (l) { Cons(h, t) => { return h.len() + count(t); }, Nil => { return 0; } } return 0; }
function g(n: i32): i32 {
    let l: L = Nil;
    if (n != 0) {
        let s: string = mk(n);
        l = two(s);
    }
    return count(l);
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`, "FERN_SEM_IR: g: a value merged past its source has no copy: L holds itself, so its copy would recurse"},
		{"closure-over-a-record-returned", `import "std/i32";
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
struct P { a: str }
function viewer(n: i32): () => i32 {
    let s: string = mk(n);
    let p: P = P { a: slice_unchecked(s, 1, 4) };
    return () => p.a.len() * 10 + (p.a[0] as i32) - 97;
}
function main(): i32 {
    let f: () => i32 = viewer(3);
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    print(f().to_string());
    return 0;
}
`, "FERN_SEM_IR: viewer: view result escapes its source"},
		{"closure-captures-a-view", `import "std/i32";
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function viewer(n: i32): () => i32 {
    let s: string = mk(n);
    let v: str = slice_unchecked(s, 1, 4);
    return () => v.len() * 10 + (v[0] as i32) - 97;
}
function main(): i32 {
    let f: () => i32 = viewer(3);
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    print(f().to_string());
    return 0;
}
`, "FERN_SEM_IR: viewer: closure capture type"},
	} {
		code, out := compile(c.src)
		if code != 3 || !strings.Contains(out, c.why) || !strings.Contains(out, "FERN_SEM_IR: the typed lowering refused") {
			t.Fatalf("%s: exit %d, want 3 naming %q\n%s", c.name, code, c.why, out)
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
    let w: Writer = stdout();
    let t: Tail = FileTail(stdin(), 1 as i64);
    return w.fd * 10 + fd_of(t) + fd_direct(stdin()) + 5;
}
`
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin"} {
		if code, out := compileFor(target, handleFd); code != 0 {
			t.Errorf("%s: a handle's fd read: exit %d\n%s", target, code, out)
		}
	}

	// The records an instance builds at its binding, on every target: a wide
	// binding a wasm field stores at 8 bytes, a record holding one, and one
	// holding itself, each held to its pinned answer.
	for _, c := range []struct{ name, src, want string }{
		{"wide-and-nested", `import "std/i32";
import "std/i64";
struct Slot[T] { v: T }
struct Pair[T] { a: Slot[T], n: i32 }
pub function keep[T](f: () => T): T {
    let c: Slot[T] = Slot[T] { v: f() };
    let p: Pair[T] = Pair[T] { a: c, n: 1 };
    return p.a.v;
}
pub function wrap[U](g: () => U): U { return keep(g); }
function main(): i32 {
    let hs: (() => i64)[] = [(): i64 => 5000000000 as i64];
    let ds: (() => f64)[] = [(): f64 => 2.5];
    print(keep(hs[0]).to_string() + " " + ((keep(ds[0]) * 2.0) as i32).to_string() + " " + wrap((): i64 => 6000000000 as i64).to_string());
    return 0;
}
`, "0|5000000000 5 6000000000\n"},
		{"recursive", `import "std/i32";
struct Node[T] { v: T, kids: Node[T][] }
pub function first[T](f: () => T): T {
    let leaf: Node[T] = Node[T] { v: f(), kids: [] };
    let root: Node[T] = Node[T] { ...leaf, kids: [leaf, leaf] };
    return root.kids[1].v;
}
function main(): i32 {
    let ss: (() => string)[] = [(): string => "q" + "r"];
    let ns: (() => i32)[] = [(): i32 => 3];
    print(first(ss[0]) + first(ns[0]).to_string());
    return 0;
}
`, "0|qr3\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
				got, report, leak := semCompileRun(t, gcc, nil, fernBin, stdlibRoot, src, target, "")
				if got != c.want {
					t.Fatalf("%s: answered %q, want %q\n%s", target, got, c.want, report)
				}
				semLeaks(t, target, leak, 0)
			}
		})
	}
}

// TestSelfHostSemIRRuntimeHelperRefusal pins the runtime-helper half: a helper
// source the typed lowering refuses fails the compile with exit 3, naming the
// helper, with no FERN_ variable set. No shipped helper is refused, so the
// driver is built from a copy whose `chr` source holds its block in an i32,
// which does not type-check.
func TestSelfHostSemIRRuntimeHelperRefusal(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	core := filepath.Join(dir, "asmcore.fern")
	src, err := os.ReadFile(core)
	if err != nil {
		t.Fatal(err)
	}
	const typed = "{ len = 0; } let p: usize = __raw_alloc(len);"
	if strings.Count(string(src), typed) != 1 {
		t.Fatalf("asmcore.fern no longer spells rt_src_chr as %q", typed)
	}
	broken := strings.Replace(string(src), typed, "{ len = 0; } let p: i32 = __raw_alloc(len);", 1)
	if err := os.WriteFile(core, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	const prog = "function main(): i32 { let s: string = chr(65); return s.len(); }\n"
	emit := func() (string, int) {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
		}
		cmd.Stdin = strings.NewReader(prog)
		cmd.Env = childEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return stderr.String(), cmd.ProcessState.ExitCode()
	}

	stderr, code := emit()
	if code != 3 || !strings.Contains(stderr, "FERN_SEM_IR: runtime __fern_chr: does not type-check") ||
		!strings.Contains(stderr, "FERN_SEM_IR: the typed lowering refused runtime helper __fern_chr") {
		t.Fatalf("exit %d, want 3 naming the refused helper\n%s", code, stderr)
	}
}
