package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var genericCtorIRCases = []struct {
	name string
	src  string
}{
	{"one_letter_struct_binds_bounded_generic", `struct P { x: i32 }
trait A { function scale(self: Self): i32; }
impl A for P { function scale(self: Self): i32 { return self.x; } }
function twice[T: A](v: T): i32 { return v.scale() + v.scale(); }
function main(): i32 { let v: P = P { x: 5 }; return twice(v) + 33; }`},
	{"array_method_chain_receiver", `import "std/array";
function inc(x: i32): i32 { return x + 1; }
function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { let a: i32[] = [1, 2]; return a.map(inc).fold(0, add) + 30; }`},
	{"empty_ctor_i32", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { let b: Box[i32] = mk(); return b.xs.len() + 40; }`},
	{"empty_ctor_string", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { let b: Box[string] = mk(); return b.xs.len() + 41; }`},
	{"nested_ctor", `struct Bag[T] { xs: T[] }
function empty[T](): Bag[T] { return Bag { xs: [] }; }
function make[T](x: T): Bag[T] { let b: Bag[T] = empty(); return b; }
function main(): i32 { let b: Bag[i32] = make(3); return b.xs.len() + 42; }`},
	{"ctor_then_use", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { let b: Box[i32] = mk(); let ys = b.xs.append(7); return ys.len() * 100 + ys[0]; }`},
	{"return_position", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function two(): Box[i32] { return mk(); }
function main(): i32 { let b = two(); return b.xs.len() + 20; }`},
	{"std_set_of", `import "std/set";
function main(): i32 { let s = set.set_of([1, 2, 2, 3, 3, 3]); return s.len(); }`},
	{"nullary_variant_payload_arg", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
function one[K, V](k: K, v: V): Tree[K, V] { return Node(Leaf, k, v, Leaf, 1); }
function main(): i32 { let t: Tree[i32, string] = one(1, "a"); match (t) { Node(l, k, v, r, s) => { return s + 40; }, Leaf => { return 0; } } }`},
	{"nullary_variant_struct_field", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
struct TMap[K, V] { root: Tree[K, V], size: i32 }
function tmap_new[K, V](): TMap[K, V] { return TMap { root: Leaf, size: 0 }; }
function h[K, V](t: Tree[K, V]): i32 { match (t) { Leaf => { return 0; }, Node(l, k, v, r, hh) => { return hh; } } }
function main(): i32 { let m: TMap[i32, string] = tmap_new(); return h(m.root) + 43; }`},
	{"recursive_indexed_self_array_payload", `enum H[T] { Leaf(T), Br(H[T][]) }
function depth[T](n: H[T]): i32 {
    match (n) {
        Leaf(x) => { return 1; },
        Br(kids) => { let d: i32 = 0; let i: i32 = 0; while (i < kids.len()) { let k: i32 = depth(kids[i]); if (k > d) { d = k; } i = i + 1; } return d + 1; },
    }
}
function main(): i32 { let a: H[i32] = Leaf(1); let b: H[i32] = Br([a, Leaf(2)]); let c: H[i32] = Br([b]); return depth(c) + 39; }`},
	{"recursive_for_in_self_array_payload", `enum H[T] { Leaf(T), Br(H[T][]) }
function depth[T](n: H[T]): i32 {
    match (n) {
        Leaf(x) => { return 1; },
        Br(kids) => { let d: i32 = 0; for k in kids { let q: i32 = depth(k); if (q > d) { d = q; } } return d + 1; },
    }
}
function main(): i32 { let a: H[i32] = Leaf(1); let b: H[i32] = Br([a, Leaf(2)]); let c: H[i32] = Br([b]); return depth(c) + 30; }`},
	{"generic_struct_field_read_arg", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
struct Split[K, V] { lt: Tree[K, V], gt: Tree[K, V] }
function size[K, V](t: Tree[K, V]): i32 { match (t) { Leaf => { return 0; }, Node(l, k, v, r, s) => { return s; } } }
function mk[K, V](k: K, v: V): Split[K, V] { return Split { lt: Node(Leaf, k, v, Leaf, 7), gt: Leaf }; }
function main(): i32 { let sp: Split[string, i32] = mk("a", 1); return size(sp.lt) * 10 + size(sp.gt) + 4; }`},
	{"match_binding_pins_recursion_own_array_param", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
function keys_into[K, V](t: Tree[K, V], own acc: K[]): K[] {
    match (t) {
        Leaf => { return acc; },
        Node(l, k, v, r, s) => { acc = keys_into(l, acc); acc = acc.append(k); acc = keys_into(r, acc); return acc; },
    }
}
function main(): i32 {
    let t: Tree[i32, string] = Node(Node(Leaf, 1, "a", Leaf, 1), 2, "b", Node(Leaf, 3, "c", Leaf, 1), 3);
    let ks: i32[] = [];
    ks = keys_into(t, ks);
    return ks.len() * 10 + ks[0] + ks[2] * 2;
}`},
	{"match_binding_pins_recursion_closure_param", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
function fold[K, V, A](t: Tree[K, V], init: A, f: (A, K, V) => A): A {
    match (t) {
        Leaf => { return init; },
        Node(l, k, v, r, s) => { let a: A = fold(l, init, f); a = f(a, k, v); return fold(r, a, f); },
    }
}
function main(): i32 { let t: Tree[string, i32] = Node(Node(Leaf, "a", 5, Leaf, 1), "b", 7, Leaf, 2); return fold(t, 30, (a: i32, k: string, v: i32) => a + v); }`},
	{"persistent_tree_p13", `import "core/cmp";
import "std/i64";
import "std/i32";
pub enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
pub struct Split[K, V] { lt: Tree[K, V], hit: Tree[K, V], gt: Tree[K, V] }
function size[K, V](t: Tree[K, V]): i32 {
    match (t) { Leaf => { return 0; }, Node(l, k, v, r, s) => { return s; } }
}
function bin[K, V](l: Tree[K, V], k: K, v: V, r: Tree[K, V]): Tree[K, V] {
    return Node(l, k, v, r, size(l) + size(r) + 1);
}
function ins[K: cmp.Ord, V](t: Tree[K, V], k: K, v: V): Tree[K, V] {
    match (t) {
        Leaf => { let e: Tree[K, V] = Leaf; return Node(e, k, v, e, 1); },
        Node(l, nk, nv, r, s) => {
            let c: i32 = k.cmp(nk);
            if (c < 0) { return bin(ins(l, k, v), nk, nv, r); }
            if (c > 0) { return bin(l, nk, nv, ins(r, k, v)); }
            return Node(l, k, v, r, s);
        },
    }
}
function keys_into[K, V](t: Tree[K, V], own acc: K[]): K[] {
    match (t) {
        Leaf => { return acc; },
        Node(l, k, v, r, s) => {
            acc = keys_into(l, acc);
            acc = acc.append(k);
            acc = keys_into(r, acc);
            return acc;
        },
    }
}
function fold[K, V, A](t: Tree[K, V], init: A, f: (A, K, V) => A): A {
    match (t) {
        Leaf => { return init; },
        Node(l, k, v, r, s) => {
            let a: A = fold(l, init, f);
            a = f(a, k, v);
            return fold(r, a, f);
        },
    }
}
function split[K: cmp.Ord, V](t: Tree[K, V], k: K): Split[K, V] {
    match (t) {
        Leaf => { let e: Tree[K, V] = Leaf; return Split { lt: e, hit: e, gt: e }; },
        Node(l, nk, nv, r, s) => {
            let c: i32 = k.cmp(nk);
            if (c < 0) {
                let sp: Split[K, V] = split(l, k);
                return Split { lt: sp.lt, hit: sp.hit, gt: bin(sp.gt, nk, nv, r) };
            }
            if (c > 0) {
                let sp: Split[K, V] = split(r, k);
                return Split { lt: bin(l, nk, nv, sp.lt), hit: sp.hit, gt: sp.gt };
            }
            return Split { lt: l, hit: t, gt: r };
        },
    }
}
function main(): i32 {
    let t: Tree[string, i32] = Leaf;
    let i: i32 = 0;
    while (i < 200) { t = ins(t, "k" + ((i * 37) % 200).to_string(), i); i = i + 1; }
    let ks: string[] = [];
    ks = keys_into(t, ks);
    let total: i32 = fold(t, 0, (a: i32, k: string, v: i32) => a + v);
    let sp: Split[string, i32] = split(t, "k5");
    let hitv: i32 = -1;
    match (sp.hit) { Node(l, k, v, r, s) => { hitv = v; }, Leaf => { hitv = -1; } }
    if (ks.len() == 200 && total == 19900 && hitv == 65 && size(sp.lt) + size(sp.gt) == 199) { return 42; }
    return 1;
}`},
	{"method_fn_param_forwarded_to_generic", `enum Tree[K, V] { Leaf, Node(K, V) }
struct M[K, V] { root: Tree[K, V] }
function mv[K, V, W](t: Tree[K, V], f: (V) => W): Tree[K, W] {
    match (t) { Leaf => { return Leaf; }, Node(k, v) => { return Node(k, f(v)); } }
}
pub function (m: M[K, V]) map_values[K, V, W](f: (V) => W): M[K, W] { return M { root: mv(m.root, f) }; }
function main(): i32 {
    let m: M[i32, i32] = M { root: Node(1, 20) };
    let n: M[i32, i32] = m.map_values((v: i32) => v + 22);
    match (n.root) { Node(k, v) => { return v; }, Leaf => { return 0; } }
}`},
	{"receiver_callback_preserves_wide_signature", `struct Box[T] { value: T }
pub function (b: Box[T]) apply(f: (T) => T): T {
    let callback: (T) => T = f;
    let run = (x: T): T => callback(x);
    let invoke = (g: (T) => T): T => g(b.value);
    return invoke(run);
}
function inc(x: i64): i64 { return x + 1i64; }
function main(): i32 {
    let b: Box[i64] = Box { value: 4294967337i64 };
    if (b.apply(inc) == 4294967338i64) { return 42; }
    return 1;
}`},
	{"receiver_returns_wide_callback", `struct Box[T] { value: T }
pub function (b: Box[T]) callback(f: (T) => T): (T) => T {
    return (x: T): T => f(x);
}
function inc(x: i64): i64 { return x + 1i64; }
function main(): i32 {
    let b: Box[i64] = Box { value: 4294967337i64 };
    let callback: (i64) => i64 = b.callback(inc);
    if (callback(b.value) == 4294967338i64) { return 42; }
    return 1;
}`},
	{"closure_named_per_receiver", `struct A[T] { xs: T[] }
struct B[T] { xs: T[] }
function keep[T](xs: T[], p: (T) => boolean): T[] {
    let out: T[] = [];
    let i: i32 = 0;
    while (i < xs.len()) { if (p(xs[i])) { out = out.append(xs[i]); } i = i + 1; }
    return out;
}
pub function (a: A[T]) filter(pred: (T) => boolean): A[T] { return A { xs: keep(a.xs, (x: T) => pred(x)) }; }
pub function (b: B[T]) filter(pred: (T) => boolean): B[T] { return B { xs: keep(b.xs, (x: T) => pred(x)) }; }
function main(): i32 {
    let a: A[i32] = A { xs: [1, 2, 3, 4, 5, 6] };
    let b: B[i32] = B { xs: [1, 2, 3, 4, 5] };
    let ea: A[i32] = a.filter((x: i32) => x % 2 == 0);
    let ob: B[i32] = b.filter((x: i32) => x % 2 == 1);
    return ea.xs.len() * 10 + ob.xs.len() + 9;
}`},
}

func TestSelfHostGenericCtorIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "gci")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range genericCtorIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "gci_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "gci_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostGenericCtorWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping generic-ctor wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range genericCtorIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s wasm = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
