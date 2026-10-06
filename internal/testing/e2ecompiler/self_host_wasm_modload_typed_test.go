package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSelfHostWasmModloadTypedLowering pins wasm_modload_run's typed build:
// every emit and link lowers the whole program through the typed lowering and
// keeps the units it asked for. The functions no module declares (the generic
// instances, a lifted closure) are the entry's and are emitted once, by the
// unit that ends the entry module. A refusal fails the emit.
func TestSelfHostWasmModloadTypedLowering(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_modload_run.fern", "wasm_modload_run")

	drive := func(t *testing.T, entry string, env []string, args ...string) (string, string, int) {
		t.Helper()
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		cmd.Env = append(os.Environ(), env...)
		var so, se strings.Builder
		cmd.Stdout = &so
		cmd.Stderr = &se
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return so.String(), se.String(), ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return so.String(), se.String(), 0
	}
	write := func(t *testing.T, path, src string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("windowed_units_link_and_run", func(t *testing.T) {
		proj := t.TempDir()
		write(t, filepath.Join(proj, "leaf.fern"), `pub function pick[T](a: T, b: T, first: boolean): T {
    if (first) { return a; }
    return b;
}

@noinline pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
`)
		entry := filepath.Join(proj, "entry.fern")
		write(t, entry, `import "./leaf";

function main(): i32 {
    let s: string = leaf.pick("ab", "xyz", false);
    let k: i32 = 3;
    return leaf.pick(4, 5, true) + s.len() + leaf.apply((x: i32): i32 => x * k, 2);
}
`)
		// The entry declares main alone; the lifted closure is the second
		// function its units cover.
		counts, se, code := drive(t, entry, nil, "-per-module-func-counts")
		if code != 0 || strings.Fields(counts)[len(strings.Fields(counts))-1] != "2" {
			t.Fatalf("-per-module-func-counts: exit %d, %q, want the entry's count 2\n%s", code, counts, se)
		}
		cacheDir := filepath.Join(proj, "cache")
		if err := os.Mkdir(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		plan := [][3]int{{0, 0, 2}, {1, 0, 1}, {1, 1, 2}}
		var planText strings.Builder
		for _, u := range plan {
			rng := strconv.Itoa(u[1]) + ":" + strconv.Itoa(u[2])
			_, se, code := drive(t, entry, []string{"FERN_SEM_IR_REPORT=1"}, "-per-module-emit", strconv.Itoa(u[0]), "-func-range", rng, "-cache-dir", cacheDir)
			if code != 0 {
				t.Fatalf("emit %v: exit %d\n%s", u, code, se)
			}
			if !strings.Contains(se, "FERN_SEM_IR: module: produced 4 of 4 declarations and 2 of 2 instances") {
				t.Fatalf("emit %v did not take the typed lowering whole:\n%s", u, se)
			}
			planText.WriteString(strconv.Itoa(u[0]) + " " + strconv.Itoa(u[1]) + " " + strconv.Itoa(u[2]) + "\n")
		}
		// Window 0:2 is the whole of leaf, so the whole-module emit is the
		// same unit and is served from it.
		if _, se, code := drive(t, entry, nil, "-per-module-emit", "0", "-cache-dir", cacheDir); code != 0 || !strings.Contains(se, "cache-hit leaf") {
			t.Fatalf("whole-module emit of leaf after its full window: exit %d, want a cache hit\n%s", code, se)
		}
		planPath := filepath.Join(proj, "plan.txt")
		write(t, planPath, planText.String())
		wat, se, code := drive(t, entry, nil, "-link", "-plan", planPath, "-cache-dir", cacheDir)
		if code != 0 {
			t.Fatalf("-link: exit %d\n%s", code, se)
		}
		for _, sym := range []string{"(func $leaf__pick$i32", "(func $leaf__pick$string", "(func $main$clo0"} {
			if n := strings.Count(wat, sym); n != 1 {
				t.Errorf("%s defined %d times in the linked module, want once", sym, n)
			}
		}
		watPath := filepath.Join(proj, "w.wat")
		wasmPath := filepath.Join(proj, "w.wasm")
		write(t, watPath, wat)
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", wasmPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		var ee *exec.ExitError
		if err := exec.Command(wasmtime, "run", wasmPath).Run(); !errors.As(err, &ee) || ee.ExitCode() != 13 {
			t.Fatalf("linked module: %v, want exit 13", err)
		}
	})

	// The record an instance builds at its binding has no declaration in any
	// module, so the units read it from the declarations the typed lowering
	// appends; an i64 field is stored at 8 bytes through it (#10827).
	t.Run("instance_records_link_and_run", func(t *testing.T) {
		proj := t.TempDir()
		write(t, filepath.Join(proj, "leaf.fern"), `pub struct Slot[T] { v: T }

pub function keep[T](f: () => T): T {
    let c: Slot[T] = Slot[T] { v: f() };
    return c.v;
}
`)
		entry := filepath.Join(proj, "entry.fern")
		write(t, entry, `import "./leaf";

function main(): i32 {
    let fs: (() => i64)[] = [(): i64 => 5000000010 as i64];
    let gs: (() => string)[] = [(): string => "ab" + "c"];
    return ((leaf.keep(fs[0]) - (5000000000 as i64)) as i32) + leaf.keep(gs[0]).len();
}
`)
		counts, se, code := drive(t, entry, nil, "-per-module-func-counts")
		if code != 0 {
			t.Fatalf("-per-module-func-counts: exit %d\n%s", code, se)
		}
		cacheDir := filepath.Join(proj, "cache")
		if err := os.Mkdir(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		var planText strings.Builder
		for idx, n := range strings.Fields(counts) {
			rng := "0:" + n
			_, se, code := drive(t, entry, nil, "-per-module-emit", strconv.Itoa(idx), "-func-range", rng, "-cache-dir", cacheDir)
			if code != 0 {
				t.Fatalf("emit %d: exit %d\n%s", idx, code, se)
			}
			planText.WriteString(strconv.Itoa(idx) + " 0 " + n + "\n")
		}
		planPath := filepath.Join(proj, "plan.txt")
		write(t, planPath, planText.String())
		wat, se, code := drive(t, entry, nil, "-link", "-plan", planPath, "-cache-dir", cacheDir)
		if code != 0 {
			t.Fatalf("-link: exit %d\n%s", code, se)
		}
		watPath := filepath.Join(proj, "w.wat")
		wasmPath := filepath.Join(proj, "w.wasm")
		write(t, watPath, wat)
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", wasmPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		var ee *exec.ExitError
		if err := exec.Command(wasmtime, "run", wasmPath).Run(); !errors.As(err, &ee) || ee.ExitCode() != 13 {
			t.Fatalf("linked module: %v, want exit 13", err)
		}
	})

	// A dyn holding a view merged past its source now takes a copy. Keep the
	// former refusal fixture as a linked execution test of that ownership path.
	t.Run("dyn_view_merged_past_source_runs", func(t *testing.T) {
		proj := t.TempDir()
		entry := filepath.Join(proj, "main.fern")
		write(t, entry, `trait Size { function size(self: Self): i32; }
struct P { a: str }
impl Size for P { function size(self: P): i32 { return self.a.len() * 10 + (self.a[0] as i32) - 97; } }
function mk(n: i32): string {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    return s;
}
function wrap(s: string): dyn Size { let p: P = P { a: slice_unchecked(s, 1, 4) }; return p; }
function g(n: i32): i32 {
    let d: dyn Size = P { a: "q" };
    if (n != 0) {
        let s: string = mk(n);
        d = wrap(s);
    }
    let junk: string[] = [];
    let i: i32 = 0;
    while (i < 50) { junk = junk.append("zz"); i = i + 1; }
    return d.size();
}
function main(): i32 { return g(3) + g(0); }
`)
		cacheDir := filepath.Join(proj, "cache")
		if err := os.Mkdir(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, se, code := drive(t, entry, nil, "-per-module-emit-all", "-cache-dir", cacheDir); code != 0 {
			t.Fatalf("emit: exit %d\n%s", code, se)
		}
		wat, se, code := drive(t, entry, nil, "-link", "-cache-dir", cacheDir)
		if code != 0 {
			t.Fatalf("link: exit %d\n%s", code, se)
		}
		watPath := filepath.Join(proj, "dyn.wat")
		wasmPath := filepath.Join(proj, "dyn.wasm")
		write(t, watPath, wat)
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", wasmPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		var ee *exec.ExitError
		if err := exec.Command(wasmtime, "run", wasmPath).Run(); !errors.As(err, &ee) || ee.ExitCode() != 57 {
			t.Fatalf("linked dyn view: %v, want exit 57", err)
		}
	})

	// A closure capturing a bare view still has no owned environment copy.
	// This pins the refusal rather than a now-supported case.
	t.Run("bare_view_closure_refusal_fails_the_emit", func(t *testing.T) {
		proj := t.TempDir()
		entry := filepath.Join(proj, "main.fern")
		write(t, entry, `function viewer(n: i32): () => i32 {
    let s: string = "ab";
    let i: i32 = 0;
    while (i < n) { s = s + "c"; i = i + 1; }
    let v: str = slice_unchecked(s, 1, 4);
    return () => v.len() * 10 + (v[0] as i32) - 97;
}
function main(): i32 { let f: () => i32 = viewer(3); return f(); }
`)
		_, se, code := drive(t, entry, nil, "-per-module-emit", "0")
		if code != 3 || !strings.Contains(se, "FERN_SEM_IR: viewer: closure capture type") {
			t.Fatalf("emit: exit %d, want 3 naming the refusal\n%s", code, se)
		}
	})

	// Recursive view-bearing types now copy through helpers. Keep the
	// former refusal as a linked execution test of the copied list.
	t.Run("recursive_view_merged_past_source_runs", func(t *testing.T) {
		proj := t.TempDir()
		entry := filepath.Join(proj, "main.fern")
		write(t, entry, `import "std/i32";
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
function main(): i32 { return g(3) + g(0); }
`)
		cacheDir := filepath.Join(proj, "cache")
		if err := os.Mkdir(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, se, code := drive(t, entry, nil, "-per-module-emit-all", "-cache-dir", cacheDir); code != 0 {
			t.Fatalf("emit: exit %d\n%s", code, se)
		}
		wat, se, code := drive(t, entry, nil, "-link", "-cache-dir", cacheDir)
		if code != 0 {
			t.Fatalf("link: exit %d\n%s", code, se)
		}
		watPath := filepath.Join(proj, "recursive.wat")
		wasmPath := filepath.Join(proj, "recursive.wasm")
		write(t, watPath, wat)
		if out, err := exec.Command(wasmtools, "parse", watPath, "-o", wasmPath).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools parse: %v\n%s", err, out)
		}
		var ee *exec.ExitError
		if err := exec.Command(wasmtime, "run", wasmPath).Run(); !errors.As(err, &ee) || ee.ExitCode() != 3 {
			t.Fatalf("linked recursive module: %v, want exit 3", err)
		}
	})

	// A function value in a record, merged past its source, has no copy and is
	// a typed refusal by name (TestSelfHostSemIRStrict).
	t.Run("refusal_fails_the_emit", func(t *testing.T) {
		proj := t.TempDir()
		entry := filepath.Join(proj, "main.fern")
		write(t, entry, `import "std/i32";
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
    return h.f() * 10 + h.n;
}
function main(): i32 { return g(3) + g(0); }
`)
		_, se, code := drive(t, entry, nil, "-per-module-emit", "0")
		if code != 3 || !strings.Contains(se, "FERN_SEM_IR: g: a value merged past its source has no copy: a function value (() => i32) has no shape to rebuild its environment by") {
			t.Fatalf("emit: exit %d, want 3 naming the refusal\n%s", code, se)
		}
	})
}
