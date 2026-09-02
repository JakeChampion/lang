package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Return-type-directed instantiation of a generic CONSTRUCTOR. A no-argument
// generic function whose type parameter appears only in its return type
// (`mk[T](): Box[T]`) is bound from the call-site annotation (`var b: Box[i32]
// = mk()`) — the arguments alone can't. The monomorphiser (parser.fern) now:
//  1. promotes such a return-only unbounded type var into type_params (it is
//     bindable from the annotation, not just from a param),
//  2. binds it via infer_inst_ret in the annotated-var position (mono_var_init),
//  3. retypes the constructor's `return Box { xs: [] }` generic struct literal
//     — whose empty-array field yields no element type to infer from — using
//     the function's concrete return type (ms_stmt StmtReturn).
//
// Without all three the template stays un-monomorphised, its `Box { xs: [] }`
// can't lower, and the whole module drops to the legacy AST emitter and
// SEGFAULTS. std/set's `set_new` → `set_of` is the real-world victim. Each case
// is oracle-checked against the interpreter and routing-pinned to "ir".
var genericCtorIRCases = []struct {
	name string
	src  string
}{
	// The minimal failing shape: a no-arg constructor with an empty-array field,
	// instantiated from the var annotation. len 0 + 40 = 40.
	{"empty_ctor_i32", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { var b: Box[i32] = mk(); return b.xs.len() + 40; }`},
	// A string element type: the clone must carry the concrete `string[]` field.
	{"empty_ctor_string", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { var b: Box[string] = mk(); return b.xs.len() + 41; }`},
	// Nested: a generic function calling ANOTHER no-arg constructor with its own
	// (already-concrete) type param supplied by the local annotation — the exact
	// `set_of` → `set_new` shape. len 0 + 42 = 42.
	{"nested_ctor", `struct Bag[T] { xs: T[] }
function empty[T](): Bag[T] { return Bag { xs: [] }; }
function make[T](x: T): Bag[T] { var b: Bag[T] = empty(); return b; }
function main(): i32 { var b: Bag[i32] = make(3); return b.xs.len() + 42; }`},
	// The constructed value is actually used: append then read back → 1*100+7.
	{"ctor_then_use", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { var b: Box[i32] = mk(); var ys = b.xs.append(7); return ys.len() * 100 + ys[0]; }`},
	// RETURN position: `return mk()` binds the constructor from the ENCLOSING
	// function's declared return type — not a var annotation. Since the return-
	// only type param is now promoted (template dropped), an unresolved call
	// here is an undefined reference, so this position must resolve too. len 0
	// + 20 = 20.
	{"return_position", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function two(): Box[i32] { return mk(); }
function main(): i32 { var b = two(); return b.xs.len() + 20; }`},
	// The real-world case: std/set's set_of builds through set_new (a no-arg
	// generic constructor) — distinct count of {1,2,3} = 3.
	{"std_set_of", `import "std/set";
function main(): i32 { var s = set.set_of([1, 2, 2, 3, 3, 3]); return s.len(); }`},
	// A payload-less variant in a PAYLOAD position of a multi-param generic
	// enum: the enum pass flows each declared field type, instantiated, into
	// the construction's args, so the bare `Leaf` pins to `Leaf__i32__string`
	// instead of dangling as an undefined function value. 1 + 40 = 41.
	{"nullary_variant_payload_arg", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
function one[K, V](k: K, v: V): Tree[K, V] { return Node(Leaf, k, v, Leaf, 1); }
function main(): i32 { var t: Tree[i32, string] = one(1, "a"); match (t) { Node(l, k, v, r, s) => { return s + 40; }, Leaf => { return 0; } } }`},
	// The same bare variant in a generic STRUCT's field position, pinned from
	// the struct clone's concrete field type. 0 + 43 = 43.
	{"nullary_variant_struct_field", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
struct TMap[K, V] { root: Tree[K, V], size: i32 }
function tmap_new[K, V](): TMap[K, V] { return TMap { root: Leaf, size: 0 }; }
function h[K, V](t: Tree[K, V]): i32 { match (t) { Leaf => { return 0; }, Node(l, k, v, r, hh) => { return hh; } } }
function main(): i32 { var m: TMap[i32, string] = tmap_new(); return h(m.root) + 43; }`},
	// A recursive generic over an enum whose payload is an ARRAY OF ITSELF:
	// the match binding `kids` is typed from the variant's declared field
	// (`H[T][]` under `H[i32]`), so the indexed element binds the recursive
	// call's T. The enum is deliberately one letter — an applied `H[...]` must
	// not read as a type variable. depth 3 + 39 = 42.
	{"recursive_indexed_self_array_payload", `enum H[T] { Leaf(T), Br(H[T][]) }
function depth[T](n: H[T]): i32 {
    match (n) {
        Leaf(x) => { return 1; },
        Br(kids) => { var d: i32 = 0; var i: i32 = 0; while (i < kids.len()) { var k: i32 = depth(kids[i]); if (k > d) { d = k; } i = i + 1; } return d + 1; },
    }
}
function main(): i32 { var a: H[i32] = Leaf(1); var b: H[i32] = Br([a, Leaf(2)]); var c: H[i32] = Br([b]); return depth(c) + 39; }`},
	// Same, iterating the payload with `for ... in`: the loop var carries the
	// array's element type. depth 3 + 30 = 33.
	{"recursive_for_in_self_array_payload", `enum H[T] { Leaf(T), Br(H[T][]) }
function depth[T](n: H[T]): i32 {
    match (n) {
        Leaf(x) => { return 1; },
        Br(kids) => { var d: i32 = 0; for k in kids { var q: i32 = depth(k); if (q > d) { d = q; } } return d + 1; },
    }
}
function main(): i32 { var a: H[i32] = Leaf(1); var b: H[i32] = Br([a, Leaf(2)]); var c: H[i32] = Br([b]); return depth(c) + 30; }`},
	// A FIELD READ of a generic-struct value (`sp.lt` on `Split[string, i32]`)
	// recovers the field's instantiated type, so `size(sp.lt)` binds K, V.
	// 7 * 10 + 0 + 4 = 74.
	{"generic_struct_field_read_arg", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
struct Split[K, V] { lt: Tree[K, V], gt: Tree[K, V] }
function size[K, V](t: Tree[K, V]): i32 { match (t) { Leaf => { return 0; }, Node(l, k, v, r, s) => { return s; } } }
function mk[K, V](k: K, v: V): Split[K, V] { return Split { lt: Node(Leaf, k, v, Leaf, 7), gt: Leaf }; }
function main(): i32 { var sp: Split[string, i32] = mk("a", 1); return size(sp.lt) * 10 + size(sp.gt) + 4; }`},
	// A match binding of the clone enum is the ONLY argument that pins K, V —
	// the callee's other params are `own K[]`, not bare K / V. 3 keys:
	// 3 * 10 + 1 + 3 * 2 = 37.
	{"match_binding_pins_recursion_own_array_param", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
function keys_into[K, V](t: Tree[K, V], own acc: K[]): K[] {
    match (t) {
        Leaf => { return acc; },
        Node(l, k, v, r, s) => { acc = keys_into(l, acc); acc = acc.append(k); acc = keys_into(r, acc); return acc; },
    }
}
function main(): i32 {
    var t: Tree[i32, string] = Node(Node(Leaf, 1, "a", Leaf, 1), 2, "b", Node(Leaf, 3, "c", Leaf, 1), 3);
    var ks: i32[] = [];
    ks = keys_into(t, ks);
    return ks.len() * 10 + ks[0] + ks[2] * 2;
}`},
	// Same with a closure param `(A, K, V) => A` and an erased accumulator
	// type param. 30 + 5 + 7 = 42.
	{"match_binding_pins_recursion_closure_param", `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V], i32) }
function fold[K, V, A](t: Tree[K, V], init: A, f: (A, K, V) => A): A {
    match (t) {
        Leaf => { return init; },
        Node(l, k, v, r, s) => { var a: A = fold(l, init, f); a = f(a, k, v); return fold(r, a, f); },
    }
}
function main(): i32 { var t: Tree[string, i32] = Node(Node(Leaf, "a", 5, Leaf, 1), "b", 7, Leaf, 2); return fold(t, 30, (a: i32, k: string, v: i32) => a + v); }`},
	// The persistent-tree acceptance program (#6794): insert / split / fold /
	// key collection over a `Tree[string, i32]`, every shape above in one
	// module. 42 when all four checks hold.
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
        Leaf => { var e: Tree[K, V] = Leaf; return Node(e, k, v, e, 1); },
        Node(l, nk, nv, r, s) => {
            var c: i32 = k.cmp(nk);
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
            var a: A = fold(l, init, f);
            a = f(a, k, v);
            return fold(r, a, f);
        },
    }
}
function split[K: cmp.Ord, V](t: Tree[K, V], k: K): Split[K, V] {
    match (t) {
        Leaf => { var e: Tree[K, V] = Leaf; return Split { lt: e, hit: e, gt: e }; },
        Node(l, nk, nv, r, s) => {
            var c: i32 = k.cmp(nk);
            if (c < 0) {
                var sp: Split[K, V] = split(l, k);
                return Split { lt: sp.lt, hit: sp.hit, gt: bin(sp.gt, nk, nv, r) };
            }
            if (c > 0) {
                var sp: Split[K, V] = split(r, k);
                return Split { lt: bin(l, nk, nv, sp.lt), hit: sp.hit, gt: sp.gt };
            }
            return Split { lt: l, hit: t, gt: r };
        },
    }
}
function main(): i32 {
    var t: Tree[string, i32] = Leaf;
    var i: i32 = 0;
    while (i < 200) { t = ins(t, "k" + ((i * 37) % 200).to_string(), i); i = i + 1; }
    var ks: string[] = [];
    ks = keys_into(t, ks);
    var total: i32 = fold(t, 0, (a: i32, k: string, v: i32) => a + v);
    var sp: Split[string, i32] = split(t, "k5");
    var hitv: i32 = -1;
    match (sp.hit) { Node(l, k, v, r, s) => { hitv = v; }, Leaf => { hitv = -1; } }
    if (ks.len() == 200 && total == 19900 && hitv == 65 && size(sp.lt) + size(sp.gt) == 199) { return 42; }
    return 1;
}`},
}

func TestSelfHostGenericCtorIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "gci")
	root, err := filepath.Abs("../../internal/stdlib")
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
			// Oracle: the native interpreter's exit code.
			_, want := runFixtureInterp(t, entry, "")
			// The whole point of the fix is that these route IR, not AST.
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

// The wasm leg: the monomorphiser fixes live in the shared parser.fern
// front-end, so the wasm IR backend must instantiate the same clones. Drives
// the full fern.fern driver with the stdlib root (several cases import) and
// runs under wasmtime. Case table shared with the x86-64 leg.
func TestSelfHostGenericCtorWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping generic-ctor wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
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
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
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
