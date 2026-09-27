package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A map whose key and value are 32-bit integers or booleans runs on core/map's
// hash table under the typed lowering (ssarc.routed_map, #9608). These cases
// pin what the routing has to keep: the whole Map surface, copy-on-write under
// an alias, negative keys (which cross into core/map's usize slot and must be
// equal slots however they were computed), u32 values past 2^31 and boolean
// columns — on every target, with every allocation returned.

// routedMapSurfaceSrc exercises every routed op: construction, insert with and
// without an alias, get / get_or / has, without, keys / values, the cursor,
// a map in a struct field, in an array, captured by a closure, and cleared.
const routedMapSurfaceSrc = `import "core/map";
import "std/i32";

struct Holder { m: Map[i32, i32], tag: i32 }

function build(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert(i + 50, (i + 50) * 3); i = i + 1; }
    return m;
}

function sum_iter(m: Map[i32, i32]): i32 {
    var s: i32 = 0;
    for (k, v) in m { s = s + k * 7 + v; }
    return s;
}

function main(): i32 {
    var m: Map[i32, i32] = build(1000);
    var alias: Map[i32, i32] = m;
    alias = alias.insert(5000, 1);
    var s: i32 = m.len() * 100000 + alias.len();
    s = s + m.get_or(50, 0) + m.get_or(123456, -9);
    match (m.get(51)) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    match (m.get(99999)) { Some(v) => { s = s + v; }, None => { s = s - 1; } }
    var r: (Map[i32, i32], boolean) = m.without(50);
    var m2: Map[i32, i32] = r.0;
    if (r.1) { s = s + 11; }
    if (m.has(50) && !m2.has(50)) { s = s + 13; }
    s = s + sum_iter(m2);
    var ks: i32[] = m2.keys();
    var vs: i32[] = m2.values();
    var ki: i32 = 0;
    while (ki < ks.len()) { s = s + ks[ki] - vs[ki]; ki = ki + 1; }
    var h: Holder = Holder { m: build(10), tag: 4 };
    s = s + h.m.len() + h.tag;
    var hs: Holder[] = [h, Holder { m: m2, tag: 1 }];
    s = s + hs[1].m.len();
    var cap: Map[i32, i32] = build(5);
    var f = (x: i32): i32 => { return cap.get_or(x, 0) + cap.len(); };
    s = s + f(52);
    var e: Map[i32, i32] = m.cleared();
    s = s + e.len();
    for (k, v) in e { s = s + k + v; }
    print(s.to_string());
    return 0;
}
`

// routedMapNegativeKeysSrc inserts keys computed in a loop and looks them up
// by literal: the two forms reach the slot through different instructions.
const routedMapNegativeKeysSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < 1000) { m = m.insert(i - 50, (i - 50) * 3); i = i + 1; }
    var got: string = "none";
    match (m.get(-49)) { Some(v) => { got = v.to_string(); }, None => {} }
    print(m.get_or(-50, 7777).to_string() + " " + got + " " + m.get_or(949, 0).to_string() + " " + m.has(-51).to_string());
    return 0;
}
`

// routedMapCowSrc: an insert or a without through an alias copies, and the
// original is untouched.
const routedMapCowSrc = `import "core/map";
import "std/i32";
function build(n: i32): Map[i32, i32] {
    var m: Map[i32, i32] = Map {};
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * 2); i = i + 1; }
    return m;
}
function main(): i32 {
    var a: Map[i32, i32] = build(100);
    var b: Map[i32, i32] = a;
    b = b.insert(7, 1000);
    var c: Map[i32, i32] = a;
    var r: (Map[i32, i32], boolean) = c.without(3);
    c = r.0;
    print(a.get_or(7, 0).to_string() + " " + b.get_or(7, 0).to_string() + " " + a.has(3).to_string() + " "
        + c.has(3).to_string() + " " + a.len().to_string() + " " + b.len().to_string() + " " + c.len().to_string());
    return 0;
}
`

// routedMapU32BoolSrc: a u32 column past 2^31 and boolean columns.
const routedMapU32BoolSrc = `import "core/map";
import "std/i32";
function main(): i32 {
    var big: Map[u32, u32] = Map {};
    big = big.insert(4000000000 as u32, 3000000000 as u32);
    big = big.insert(1 as u32, 2 as u32);
    var sum: u64 = 0 as u64;
    for (k, v) in big { sum = sum + (k as u64) + (v as u64); }
    var flags: Map[boolean, boolean] = Map {};
    flags = flags.insert(true, false);
    flags = flags.insert(false, true);
    flags = flags.insert(true, true);
    var trues: i32 = 0;
    for v in flags.values() { if (v) { trues = trues + 1; } }
    for k in flags.keys() { if (k) { trues = trues + 10; } }
    print((big.get_or(4000000000 as u32, 0 as u32) == (3000000000 as u32)).to_string() + " "
        + (sum == (7000000003 as u64)).to_string() + " " + flags.len().to_string() + " " + trues.to_string());
    return 0;
}
`

func TestSelfHostRoutedScalarMaps(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	selfHostBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	cases := []struct{ name, src, want string }{
		{"surface", routedMapSurfaceSrc, "104398092"},
		{"negative_keys", routedMapNegativeKeysSrc, "-150 -147 2847 false"},
		{"cow", routedMapCowSrc, "14 1000 true false 100 100 99"},
		{"u32_bool", routedMapU32BoolSrc, "true true 2 12"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, c.src, target, "FERN_SANITIZE=1")
					if stdout != c.want {
						t.Fatalf("stdout = %q, want %q\n%s", stdout, c.want, stderr)
					}
					if target == "x86-64-linux" && !strings.Contains(stderr, "leakcheck:") {
						t.Fatalf("no leak census line\n%s", stderr)
					}
					if strings.Contains(stderr, "fern-sanitizer:") || (strings.Contains(stderr, "leakcheck:") && !strings.Contains(stderr, "live_bytes=0")) {
						t.Fatalf("heap finding:\n%s", stderr)
					}
				})
			}
		})
	}

	// The routed program calls core/map and nothing of the runtime's map.
	asm := routedMapAsm(t, selfHostBin, stdlibRoot, routedMapSurfaceSrc)
	if !strings.Contains(asm, "call __fn___map_set_impl") || strings.Contains(asm, "call __fern_map_set") {
		t.Fatal("the surface program's maps are not routed onto core/map")
	}

	// A bisect knob keeps a mixed module, so under one every map stays on the
	// runtime and the program still runs.
	t.Run("bisect_knob", func(t *testing.T) {
		asm := routedMapAsm(t, selfHostBin, stdlibRoot, routedMapSurfaceSrc, "FERN_SEM_IR_SKIP=__no_such_function__")
		if strings.Contains(asm, "call __fn___map_set_impl") {
			t.Fatal("a map was routed under FERN_SEM_IR_SKIP")
		}
		stdout, stderr := routedMapRun(t, selfHostBin, stdlibRoot, routedMapSurfaceSrc, "x86-64-linux", "FERN_SEM_IR_SKIP=__no_such_function__")
		if stdout != "104398092" {
			t.Fatalf("stdout = %q, want 104398092\n%s", stdout, stderr)
		}
	})
}

func routedMapAsm(t *testing.T, fernBin, stdlibRoot, src string, env ...string) string {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog.s")
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", in, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// routedMapRun compiles src for target with the typed lowering held to
// production (FERN_SEM_IR_STRICT), runs it and returns trimmed stdout and
// stderr. `env` joins the compile's environment.
func routedMapRun(t *testing.T, fernBin, stdlibRoot, src, target string, env ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog")
	args := []string{"-target", target, in, stdlibRoot, "-o", out}
	if target == "wasm32-wasi" {
		out = filepath.Join(dir, "prog.wat")
		args = []string{"-target", target, "-emit", "asm", in, stdlibRoot, "-o", out}
	}
	cmd := exec.Command(fernBin, args...)
	cmd.Env = append(append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_STRICT=1"), env...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile (%s): %v\n%s", target, err, msg)
	}
	var run *exec.Cmd
	switch target {
	case "x86-64-linux":
		run = exec.Command(out)
	case "arm64-linux":
		_, qemu := arm64Tooling(t)
		run = runArm64Bin(qemu, out)
	default:
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Fatal("wasmtime not on PATH")
		}
		run = exec.Command("wasmtime", "run", out)
	}
	var stdout, stderr strings.Builder
	run.Stdout = &stdout
	run.Stderr = &stderr
	if err := run.Run(); err != nil {
		t.Fatalf("run (%s): %v\n%s", target, err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), stderr.String()
}

// mapRoutingUnitDriver lowers `tally` through the typed pipeline and prints
// whether its unit routes maps and every call the lowering makes.
const mapRoutingUnitDriver = `import "./semsource"; import "./ssarc"; import "./ssaunits"; import "./ir";
import "./parser"; import "./lexer"; import "./irlower"; import "./checker";
function main(): i32 {
    var src: string = "";
    match (read_file(args()[1])) { Ok(text) => { src = text; }, Err(_) => { return 2; } }
    var parsed = parser.parse_module(lexer.tokenize(src));
    var typed = irlower.lift_lambdas_typed(checker.annotate_module(parser.register_struct_method_generics(parser.register_map_method_generics(parser.register_array_method_generics(parser.Module { ...parsed, structs: parser.inject_builtin_enums(parsed.structs) })))));
    var tab = irlower.struct_tab(parser.erase_str_module(typed).structs);
    var built = semsource.build_module(typed);
    var call: i32 = ir.op_call_direct("", 0).kind_tag;
    var at: i32 = 0;
    for fd in typed.funcs {
        if (fd.name == "tally") {
            var p = built.decls[at];
            if (!p.ok) { eprint(p.why); return 4; }
            var plan = ssaunits.plan(p.func, p.modes);
            if (!plan.ok) { eprint(plan.why); return 5; }
            var lowered = ssarc.lower(p.func, p.modes, plan, tab, []);
            if (!lowered.ok) { eprint(lowered.why); return 6; }
            if (p.func.routes_maps) { print("routes_maps true"); } else { print("routes_maps false"); }
            for op in lowered.ops { if (op.kind_tag == call) { print("call " + op.str); } }
        }
        at = at + 1;
    }
    return 0;
}
`

const mapRoutingTally = `function tally(n: i32): i32 {
    var m: Map[i32, i32] = map_new(4);
    var i: i32 = 0;
    while (i < n) { m = m.insert(i, i * i); i = i + 1; }
    return m.get_or(2, 0) + m.len();
}
`

// TestSelfHostMapRoutingFollowsUnit: a map runs on core/map only in a unit
// that defines every core/map function routing calls. A unit without them —
// a test driver with no module loader, a runtime helper source — keeps the
// runtime's map, or the link names symbols nothing defines (#10510).
func TestSelfHostMapRoutingFollowsUnit(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "map_routing_unit.fern"), []byte(mapRoutingUnitDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "map_routing_unit.fern", "map-routing-unit")
	// Stand-ins for core/map: routing reads which functions the unit defines,
	// never their bodies.
	var stubs strings.Builder
	for _, name := range []string{"map_new_impl", "__map_set_impl", "__map_len_impl", "__map_has_impl", "__map_get_impl",
		"__map_get_or_impl", "__map_delete_impl", "__map_cow_inplace", "__map_i32_column",
		"__map_bool_column", "__map_drop_impl", "__map_iter_impl", "__mapiter_has_next_impl",
		"__mapiter_key_impl", "__mapiter_value_impl", "__mapiter_advance_impl", "__mapiter_drop_impl"} {
		stubs.WriteString("function " + name + "(): i32 { return 0; }\n")
	}
	for _, c := range []struct {
		name, src string
		routed    bool
	}{
		{"without_core_map", mapRoutingTally, false},
		{"with_core_map", mapRoutingTally + stubs.String(), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := runX86_64Bin(runner, driver, src).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, got)
			}
			out := string(got)
			if want := fmt.Sprintf("routes_maps %v\n", c.routed); !strings.HasPrefix(out, want) {
				t.Fatalf("want %q first:\n%s", want, out)
			}
			for _, sym := range []string{"call map_new_impl\n", "call __map_set_impl\n", "call __map_drop_impl\n"} {
				if strings.Contains(out, sym) != c.routed {
					t.Fatalf("%q present = %v, want %v:\n%s", strings.TrimSpace(sym), !c.routed, c.routed, out)
				}
			}
		})
	}
}
