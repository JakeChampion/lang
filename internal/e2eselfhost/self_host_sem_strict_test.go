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

	// A read of a view map value would hand out the column's own view box, whose
	// retain is a no-op, so the reader's release freed the map's entry.
	viewRead := `import "core/map";
function main(): i32 {
    var b: string = "abcdefgh";
    var m: Map[i32, str] = map_new(4);
    m = m.insert(1, slice_unchecked(b, 2, 6));
    match (m.get(1)) { Some(v) => { return v.len(); }, None => { return 0; } }
}
`
	code, out := compile(viewRead, "FERN_SEM_IR_STRICT=1")
	if code != 3 || !strings.Contains(out, "FERN_SEM_IR: main: a read of a view map value would share the column's view box") || !strings.Contains(out, "FERN_SEM_IR_STRICT") {
		t.Fatalf("a view map value read: exit %d under strict, want 3 naming the refusal\n%s", code, out)
	}
	if code, out := compile(viewRead, "FERN_SEM_IR_STRICT="); code != 0 {
		t.Fatalf("a refused module without strict: exit %d, want the AST lowering's compile\n%s", code, out)
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

	// A value that holds a view behind a dyn cannot be rebuilt at a merge
	// (ssasem.copyable), so one merged past its source is refused rather than
	// read after the source is released. A closure that captures a view is
	// refused where it is built.
	for _, c := range []struct{ name, src, why string }{
		{"dyn-view-merged", `import "std/i32";
trait Size { function size(self: Self): i32; }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    var s: string = "ab";
    var i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function wrap(s: string): dyn Size { var p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
function g(n: i32): i32 {
    var d: dyn Size = P { a: "q" };
    if (n != 0) {
        var s: string = mk(n);
        d = wrap(s);
    }
    var junk: string[] = [];
    var i: i32 = 0;
    while (i < 50) { junk = junk.append("zz" + i.to_string()); i = i + 1; }
    return d.size();
}
function main(): i32 { print(g(3).to_string() + " " + g(0).to_string()); return 0; }
`, "FERN_SEM_IR: g: produced graph fails semantic verification: dependency unavailable at use"},
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
