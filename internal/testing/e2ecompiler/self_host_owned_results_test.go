package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func checkedOwnedResultCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const prefix = `struct Box { values: u8[] }
function take(own box: Box): Box { return box; }
function (own box: Box) close(): void {}
`
	for _, tc := range []struct{ name, body, code string }{
		{"owned enum state", `enum State { Plain, Secured(Box), Awaiting(Option[Box]) } function awaiting(own state: State): own State { match (state) { Secured(box) => { return Awaiting(Some(box)); }, _ => { return Awaiting(None); } } return Awaiting(None); }`, ""},
		{"borrowed enum state", `enum State { Plain, Secured(Box), Awaiting(Option[Box]) } function awaiting(state: State): own State { match (state) { Secured(box) => { return Awaiting(Some(box)); }, _ => { return Awaiting(None); } } return Awaiting(None); }`, "E051"},
		{"fresh match result", `enum State { Plain, Secured(Box) } function fresh(state: State): own Box { match (state) { Secured(box) => { return Box { values: [box.values.len() as u8] }; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }`, ""},
		{"fresh match shadow", `enum State { Plain, Secured(Box) } function fresh(state: State): own Box { match (state) { Secured(box) => { let box = Box { values: [] }; return box; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }`, ""},
		{"borrowed match alias", `enum State { Plain, Secured(Box) } function unwrap(state: State): own Box { match (state) { Secured(box) => { let local = box; return local; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }`, "E051"},
		{"borrowed nested match", `enum State { Plain, Secured(Box) } function unwrap(state: Option[State]): own Box { match (state) { Some(s) => { match (s) { Secured(box) => { return box; }, _ => {} } }, _ => {} } return Box { values: [] }; }`, "E051"},
		{"borrowed match shadow", `enum State { Plain, Secured(Box) } function unwrap(state: State, own box: Box): own Box { match (state) { Secured(box) => { return box; }, _ => { return box; } } return box; }`, "E051"},
		{"borrowed match capture", `enum State { Plain, Secured(Box) } function outer(state: State): void { let get = (): own Box => { match (state) { Secured(box) => { return box; }, _ => { return Box { values: [] }; } } return Box { values: [] }; }; }`, "E051"},
		{"fresh scalar field", `function fresh(x: Box): own Box { return Box { values: [x.values.len() as u8] }; }`, ""},
		{"checked value copy", `@value struct Key { bytes: u8[] } function (own k: Key) erase(): void {} function copy(k: Key): own Key { return k; }`, ""},
		{"method result", `function (x: Box) fresh(): own Box { return Box { values: [] }; } function pass(x: Box): own Box { return x.fresh(); }`, ""},
		{"call copy direct array", `function loan(x: i32[]): i32[] { return x; } function consume(own x: i32[]): void {} function run(x: i32[]): void { consume(loan(x)); print(x.len()); }`, ""},
		{"call copy local array", `function loan(x: i32[]): i32[] { return x; } function consume(own x: i32[]): void {} function run(x: i32[]): void { let y = loan(x); consume(y); print(x.len()); }`, ""},
		{"call copy callback result", `function run(x: i32[], f: (i32[]) => i32[]): void { consume(f(x)); print(x.len()); } function consume(own x: i32[]): void {}`, ""},
		{"call copy nested data", `struct Data { bytes: u8[] } function loan(x: Data): Data { return x; } function consume(own x: Data): void {} function run(x: Data): void { let y = loan(x); consume(y); print(x.bytes.len()); }`, ""},
		{"call copy checked value", `@value struct Key { bytes: u8[] } function (own x: Key) close(): void {} function loan(x: Key): Key { return x; } function consume(own x: Key): void {} function run(x: Key): void { consume(loan(x)); print(x.bytes.len()); }`, ""},
		{"call copy resource", `struct H { fd: i32 } function (own h: H) close(): void {} function loan(x: H): H { return x; } function consume(own x: H): void {} function run(x: H): void { consume(loan(x)); }`, "E051"},
		{"call copy resource array", `struct H { fd: i32 } function (own h: H) close(): void {} function loan(x: H[]): H[] { return x; } function consume(own x: H[]): void {} function run(x: H[]): void { let y = loan(x); consume(y); }`, "E051"},
		{"call copy cell", `function loan(x: Cell[i32]): Cell[i32] { return x; } function consume(own x: Cell[i32]): void {} function run(x: Cell[i32]): void { consume(loan(x)); }`, "E051"},
		{"call copy callback", `function loan(x: () => i32): () => i32 { return x; } function consume(own x: () => i32): void {} function run(x: () => i32): void { consume(loan(x)); }`, "E051"},
		{"call copy unconstrained generic", `function loan[T](x: T): T { return x; } function consume[T](own x: T): void {} function run[T](x: T): void { consume(loan(x)); }`, "E051"},
		{"resource counterpart e051-result-is-the-borrowed-parameter", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function passthru(xs: H[]): H[] { return xs; }
function borrowed_source(source: H[]): i32 { return keep(passthru(source)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-local", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function hop(xs: H[]): H[] {
    let y: H[] = xs;
    return y;
}
function borrowed_source(source: H[]): i32 { return keep(hop(source)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-chain", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function passthru(xs: H[]): H[] { return xs; }
function relay(ys: H[]): H[] { return passthru(ys); }
function borrowed_source(source: H[]): i32 { return keep(relay(source)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-generic-callback", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function fold[A](xs: H[], acc: A, f: (H[], A) => A): A { return f(xs, acc); }
function pick(xs: H[], acc: H[]): H[] { return xs; }
function collect(xs: H[]): H[] { let none: H[] = []; return fold(xs, none, pick); }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(collect(xs)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-function-parameter", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function via(xs: H[], f: (H[]) => H[]): H[] { return f(xs); }
function same(xs: H[]): H[] { return xs; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(via(xs, same)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-closure-local", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function via(xs: H[]): H[] {
    let f: (H[]) => H[] = (a: H[]): H[] => a;
    return f(xs);
}
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(via(xs)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-bounded-generic", `struct H { fd: i32 } function (own h: H) close(): void {}
trait Named {
  function name(self: Self): string;
}
struct N { s: string }
impl Named for N {
  function name(self: N): string { return self.s; }
}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function first[T: Named](t: T, xs: H[]): H[] { return xs; }
function relay(xs: H[]): H[] { return first(N { s: "a" }, xs); }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(relay(xs)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-value-if", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(c: boolean, xs: H[], ys: H[]): H[] { return if (c) { xs } else { ys }; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; let ys: H[] = []; return keep(pick(true, xs, ys)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-an-else-if-arm", `struct H { fd: i32 } function (own h: H) close(): void {}
function mk(): H[] { return [H { fd: 2 }]; }
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(a: i32, xs: H[]): H[] { return if (a == 1) { mk() } else if (a == 2) { xs } else { mk() }; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(pick(1, xs)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-value-if-local", `struct H { fd: i32 } function (own h: H) close(): void {}
function mk(): H[] { return [H { fd: 2 }]; }
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(c: boolean, xs: H[]): H[] {
    let r: H[] = if (c) { xs } else { mk() };
    return r;
}
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(pick(true, xs)); }
`, "E051"},
		{"resource counterpart e051-result-borrowed-through-a-value-match", `struct H { fd: i32 } function (own h: H) close(): void {}
enum E { A, B }
function keep(own ys: H[]): i32 { return ys[0].fd; }
function pick(e: E, xs: H[], ys: H[]): H[] { return match (e) { E.A => xs, E.B => ys }; }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; let ys: H[] = []; return keep(pick(E.A, xs, ys)); }
`, "E051"},
		{"resource counterpart e051-generic-returning-its-type-parameter-is-not-fresh", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys.len(); }
function mk(): H[] { return [H { fd: 1 }]; }
function call[T](f: () => T): T { return f(); }
function borrowed_source(source: H[]): i32 { let factory = (): H[] => source; return keep(call(factory)); }
`, "E051"},
		{"resource counterpart e051-written-iife-retains-borrowed-array", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys[0].fd; }
function id(xs: H[]): H[] { return ((): H[] => xs)(); }
function borrowed_source(source: H[]): i32 { let xs: H[] = source; return keep(id(xs)); }
`, "E051"},
		{"resource counterpart e051-an-element-that-is-a-pointer-still-borrows", `struct H { fd: i32 } function (own h: H) close(): void {}
function keep(own ys: H[]): i32 { return ys.len(); }
function firsts(xs: H[]): H[] {
    let out: H[] = [];
    out = out.append(xs[0]);
    return out;
}
function borrowed_source(source: H[]): i32 { return keep(firsts(source)); }
`, "E051"},

		{"generic data copy", `struct Data[T] { value: T } function loan[T](x: Data[T]): Data[T] { return x; } function consume(own x: Data[i32]): void {} function run(x: Data[i32]): void { let y = loan(x); consume(y); print(x.value); }`, ""},
		{"data field copy", `struct Wv { v: i32[] } function inner(w: Wv): i32[] { return w.v; } function consume(own xs: i32[]): i32 { return xs.len(); } function f(w: Wv): i32 { return consume(inner(w)); }`, ""},
		{"data function strengthening", `function loan(x: i32[]): i32[] { return x; } function run(): void { let f: (i32[]) => own i32[] = loan; }`, "E003"},
		{"reused generic cell", `struct Data[T] { value: T } struct Mixed { first: Data[i32], second: Data[Cell[i32]] } function loan(x: Mixed): Mixed { return x; } function consume(own x: Mixed): void {} function run(x: Mixed): void { consume(loan(x)); }`, "E051"},
		{"shadowed data name", `struct Data { bytes: u8[] } function loan[Data](x: Data): Data { return x; } function consume[Data](own x: Data): void {} function run[Data](x: Data): void { consume(loan(x)); }`, "E051"},
		{"copy cache concrete first", `struct Data { bytes: u8[] } struct Wrap[T] { value: T } function loan_concrete(x: Wrap[Data]): Wrap[Data] { return x; } function consume_concrete(own x: Wrap[Data]): void {} function concrete(x: Wrap[Data]): void { consume_concrete(loan_concrete(x)); } function loan_generic[Data](x: Wrap[Data]): Wrap[Data] { return x; } function consume_generic[Data](own x: Wrap[Data]): void {} function generic[Data](x: Wrap[Data]): void { consume_generic(loan_generic(x)); } `, "E051"},
		{"copy cache generic first", `struct Data { bytes: u8[] } struct Wrap[T] { value: T } function loan_generic[Data](x: Wrap[Data]): Wrap[Data] { return x; } function consume_generic[Data](own x: Wrap[Data]): void {} function generic[Data](x: Wrap[Data]): void { consume_generic(loan_generic(x)); } function loan_concrete(x: Wrap[Data]): Wrap[Data] { return x; } function consume_concrete(own x: Wrap[Data]): void {} function concrete(x: Wrap[Data]): void { consume_concrete(loan_concrete(x)); } `, "E051"},
		{"conditional result", `function pass[T](own x: T, b: boolean): own T { return if (b) { x } else { x }; }`, ""},
		{"match result", `function pass[T](own x: T, b: boolean): own T { return match (b) { true => x, false => x }; }`, ""},
		{"local function capture", `function run(own x: Box): void { function f(): own Box { return x; } }`, "E051"},
		{"borrowed conditional", `function pass(x: Box, b: boolean): own Box { return if (b) { x } else { Box { values: [] } }; }`, "E051"},
		{"bound factory", `trait Factory { function make(): own Self; } impl Factory for Box { function make(): own Self { return Box { values: [] }; } } function make[T: Factory](): own T { return T.make(); }`, ""},
		{"trait promise", `trait Factory { function make(): own Self; } impl Factory for Box { function make(): Self { return Box { values: [] }; } }`, "E021"},
		{"copied array element", `struct Data { bytes: u8[] } function wrap(data: Data): own Data[] { return [data]; }`, ""},
		{"copied tuple field", `struct Data { bytes: u8[] } function wrap(data: Data): own (Data, i32) { return (data, 1); }`, ""},
		{"copied record field", `struct Data { bytes: u8[] } struct Outer { data: Data } function wrap(data: Data): own Outer { return Outer { data: data }; }`, ""},
		{"copied captured element", `struct Data { bytes: u8[] } function wrap(data: Data): void { let f = (): own Data[] => [data]; }`, ""},
		{"fresh local", `function make(): own Box { let box = Box { values: [] }; return box; }`, ""},
		{"fresh lambda local", `function run(): void { let f = (): own Box => { let box = Box { values: [] }; return box; }; }`, ""},
		{"owned field", `struct Outer { box: Box } function get(own outer: Outer): own Box { return outer.box; }`, ""},
		{"borrowed local alias", `function bad(box: Box): own Box { let local = box; return local; }`, "E051"},
		{"captured local", `function make(): own Box { let box = Box { values: [] }; let f = (): Box => box; return box; }`, "E051"},
		{"deferred field", `struct Outer { box: Box } function (own o: Outer) close(): void { o.box.close(); } function get(own o: Outer): own Box { defer o.close(); return o.box; }`, "E051"},
		{"deferred sibling", `struct Pair { first: Box, second: Box } function get(own p: Pair): own Box { defer p.first.close(); return p.second; }`, ""},
		{"deferred local root", `function get(): own Box { let b = Box { values: [] }; defer b.close(); return b; }`, "E051"},
		{"deferred local field", `struct Outer { box: Box } function (own o: Outer) close(): void { o.box.close(); } function get(): own Box { let o = Outer { box: Box { values: [] } }; defer o.close(); return o.box; }`, "E051"},
		{"generic input", `function pass[T](own x: T): own T { return x; }`, ""},
		{"callback result", `function pass[T](own x: T, f: (own T) => own T): own T { return f(x); }`, ""},
		{"lambda result", `function run(): void { let f: (own Box) => own Box = (own x: Box): own Box => x; }`, ""},
		{"weaken result", `function pass(own x: Box): own Box { return x; } function run(): void { let f: (own Box) => Box = pass; }`, ""},
		{"nested aggregate capture", `function run(x: Box): void { let f = (): own Box[] => { let xs = [x]; return xs; }; }`, "E051"},
		{"copied f64 lanes", `function copy(x: f64x2): own f64x2 { return x; }`, ""},
		{"copied f32 lanes", `function copy(x: f32x4): own f32x4 { return x; }`, ""},
		{"copied map root", `function copy(m: Map[string, boolean]): own Map[string, boolean] { return m; }`, ""},
		{"copied map update", `function update(own m: Map[string, boolean]): own Map[string, boolean] { return m.insert("k", true); }`, ""},
		{"copied nested map", `struct Data { entries: Map[string, boolean] } function copy(d: Data): own Data { return d; }`, ""},
		{"borrowed map resource", `function bad(m: Map[string, Box]): own Map[string, Box] { return m; }`, "E051"},
		{"borrowed map cell", `function bad(m: Map[string, Cell[i32]]): own Map[string, Cell[i32]] { return m; }`, "E051"},
		{"borrowed map callback", `function bad(m: Map[string, () => i32]): own Map[string, () => i32] { return m; }`, "E051"},
		{"copied data root", `struct Data { bytes: u8[] } function copy(data: Data): own Data { return data; }`, ""},
		{"borrowed mutable root", `function bad(data: Cell[i32]): own Cell[i32] { return data; }`, "E051"},
		{"borrowed receiver", `function (x: Box) bad(): own Box[] { return [x]; }`, "E051"},
		{"aggregate loan", `function pass(x: Box): own Box[] { return [x]; }`, "E051"},
		{"aggregate local loan", `function pass(x: Box): own Box[] { let xs = [x]; return xs; }`, "E051"},
		{"aggregate helper loan", `function wrap(x: Box): Box[] { return [x]; } function pass(x: Box): own Box[] { return wrap(x); }`, "E051"},
		{"borrowed input", `function pass[T](x: T): own T { return x; }`, "E051"},
		{"borrowed lambda input", `function run(): void { let f = (x: Box): own Box => x; }`, "E051"},
		{"borrowed capture", `function run(x: Box): void { let f = (): own Box => x; }`, "E051"},
		{"owned capture", `function run(own x: Box): void { let f = (): own Box => x; }`, "E051"},
		{"strengthen callback", `function pass(own x: Box): Box { return x; } function run(): void { let f: (own Box) => own Box = pass; }`, "E003"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + tc.body + ` function main(): i32 { return 0; }`
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if tc.code == "" && len(ds) == 0 {
					return
				}
				for _, d := range ds {
					if tc.code != "" && d.code == tc.code {
						return
					}
				}
				t.Fatalf("%s: want %q, got %v", label, tc.code, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(source)
			check("Fern", driverDiags(runCheckerDriver(t, cmd, tc.name)))
		})
	}
}

func checkedOwnedResultBorrowFixtures(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	for _, name := range []string{
		"diag_e051_literal_match_arm_returns_a_borrow",
		"diag_e051_match_binder_of_a_borrowed_scrutinee",
		"diag_e051_owned_result_borrowed_match_payload",
		"diag_e051_through_a_type_parameter",
	} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(e2eharness.RepoPath("conformance", "cases", name, "main.fern"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = bytes.NewReader(source)
			for label, ds := range map[string][]driverDiag{"Go": goCheckerDiags(t, dir, string(source)), "Fern": driverDiags(runCheckerDriver(t, cmd, name))} {
				found := false
				for _, d := range ds {
					found = found || d.code == "E051"
				}
				if !found {
					t.Fatalf("%s: want E051, got %v", label, ds)
				}
			}
		})
	}
}

func TestSelfHostCheckedOwnedResultsNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_results_checker")
	checkedOwnedResultCases(t, bin, runner, dir)
	checkedOwnedResultBorrowFixtures(t, bin, runner, dir)
}

func TestSelfHostCheckedOwnedResultsWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_results_checker.wasm", e2eharness.TargetWasm32Wasi)
	checkedOwnedResultCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
	checkedOwnedResultBorrowFixtures(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostCheckedOwnedResultsRun(t *testing.T) {
	ownedResultRuntimeCase(t, "checked_owned_results")
}

func TestSelfHostCopiedCallResultsRun(t *testing.T) {
	ownedResultRuntimeCase(t, "copied_call_results")
}

func ownedResultRuntimeCase(t *testing.T, name string) {
	t.Helper()
	cli := buildSelfHostCLI(t)
	path := e2eharness.RepoPath("conformance", "cases", name, "main.fern")
	want, err := os.ReadFile(filepath.Join(filepath.Dir(path), "expected.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			var cmd *exec.Cmd
			if target == e2eharness.TargetWasm32Wasi {
				bin := cli.wasmComponent(t, path, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
				cmd = exec.Command(e2eharness.Wasmtime(t), "run", bin)
			} else if target == e2eharness.TargetArm64Linux {
				_, qemu := arm64Tooling(t)
				bin := cli.arm64Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				cmd = runArm64Bin(qemu, bin)
			} else {
				bin := cli.x86Binary(t, path, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				cmd = runX86_64Bin(cli.runner, bin)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil || !bytes.Equal(out, want) {
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, out, stderr.String())
			}
			var allocs, frees, live int64
			summary := leakSummaryLine(stderr.String())
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
				t.Fatalf("invalid allocation census: %q (%v)", summary, err)
			}
		})
	}
}
