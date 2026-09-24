package e2eselfhost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/ast"
)

// The self-host register path (docs/SELFHOST-SSA-BACKEND.md): every function
// is lifted to SSA form and emitted with registers; it is the only emitter on
// the native ISAs, so a function it cannot take is a compile error, never a
// fall-through. These tests build the self-host CLI for this host once,
// compile each program with it and with the native compiler for every target
// the host can run output for (its own ISA natively, the other under its qemu
// user emulator when present), run both, and compare stdout and exit status.
// Each program is a shape the register path once got wrong or emits specially,
// named in the comment above it.

// ssaBackendProgram is one differential case.
type ssaBackendProgram struct {
	name string
	src  string
}

var ssaBackendPrograms = []ssaBackendProgram{
	{name: "fact", src: `
function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }
function main(): i32 { return fact(5) - 100; }
`},
	{name: "fib_swap", src: `
function fib(n: i32): i32 {
    var a: i32 = 0;
    var b: i32 = 1;
    var i: i32 = 0;
    while (i < n) { var t: i32 = a; a = b; b = t + b; i = i + 1; }
    return a;
}
function main(): i32 { return fib(10) % 256; }
`},
	{name: "wide_and_casts", src: `
function mix(x: i64, y: i64): i64 { return (x * y) / 7 + (x % 5) - (y << 3); }
function narrow(v: i64): i32 { return (v as i32) & 255; }
function shifts(a: i32, k: i32): i32 { return ((a << k) | (a >> 1)) ^ (((a as u32) >> 2) as i32); }
function unsigned(a: u32, b: u32): i32 { if (a < b) { return 1; } return 0; }
function main(): i32 {
    var m: i64 = mix(123456789, 987654321);
    var s: i32 = shifts(1000, 3) + shifts(7, 40);
    var u: i32 = unsigned(4000000000, 5) * 10 + unsigned(5, 4000000000);
    var r: i32 = narrow(m) + s + u;
    return r & 127;
}
`},
	// A construction that reuses a dead box of another type through a Perceus
	// token: ssarc's reuse_construct writes the recipient's own shape word over
	// the donor's with struct_set_shape, since neither box arrives carrying it.
	// That op bailed the production lift, and it was 116 of the 117 functions
	// the self-build declined — every one of them a reuse site like this.
	{name: "reuse_cross_type_shape", src: `
struct P { x: i32, y: i32 }
struct Q { a: i32, b: i32 }
function f(): i32 {
    var p: P = P { x: 1, y: 2 };
    var t: i32 = p.x + p.y;
    var q: Q = Q { a: 3, b: 4 };
    return t + q.a + q.b;
}
function main(): i32 { return f(); }
`},
	// heap_bump_bytes is the last op the production lift had no arm for, and
	// util__arr_push_cliff_report was the one function on the self-build still
	// declining for it. It reads two runtime globals and guards the
	// uninitialised cursor, so it is not a single global's word -- it goes
	// through the flat op, which re-emits the stack machine's own sequence.
	// The check is ordering only, so both builds print the same thing.
	{name: "heap_bump_bytes", src: `
function report(): i32 {
    var before: i64 = __heap_bump_bytes();
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < 500) { xs = xs.append(i); i = i + 1; }
    var after: i64 = __heap_bump_bytes();
    if (before > after) { return 1; }
    if (after == 0i64) { return 2; }
    if (xs.len() != 500) { return 3; }
    return 0;
}
function main(): i32 { return report(); }
`},
	{name: "control_flow", src: `
function first_square_over(limit: i32): i32 {
    var i: i32 = 0;
    while (i < 1000) {
        if (i * i > limit) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}
function nested(n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var j: i32 = 0;
        while (j < n) {
            if (j == 2) { j = j + 1; continue; }
            if (i + j > 7) { break; }
            t = t + i * j;
            j = j + 1;
        }
        i = i + 1;
    }
    return t;
}
function signs(a: i32): i32 {
    if (a < 0) { return 0 - 1; } else if (a == 0) { return 0; } else { return 1; }
}
function main(): i32 {
    var d: i32 = (0 - 2147483647 - 1) / (0 - 1);
    var e: i32 = (0 - 7) % 3;
    var k: i32 = first_square_over(50) + nested(5) + signs(0 - 9) + signs(0) + signs(4) + (d % 7) + e;
    if (!(k > 1000)) { k = k + 100; }
    return k & 255;
}
`},
	// A labelled continue out of an inner loop that never exits: the inner
	// loop's exit path is unreachable, thread_forwarding drops it, and the
	// outer header's phis must lose the operand slot for that vanished
	// predecessor rather than carry a value nothing defines (the allocator
	// once indexed a block at -1 for it). count_up carries a parameter
	// through a header phi, the entry operand with no defining instruction.
	{name: "labelled_continue_defer", src: `
function count_up(i: i32, n: i32): i32 {
    while (i < n) { i = i + 1; }
    return i;
}
function main(): i32 {
    var seen: i32 = 0;
    var i: i32 = 0;
    outer: while (i < 3) {
        i = i + 1;
        loop {
            var items: i32[] = [0];
            defer seen = seen * 3 + items[0];
            items = [i];
            continue outer;
        }
    }
    return seen + count_up(4, 9);
}
`},
	// A loop-invariant parameter carried through the header phi while the
	// body calls out: the phi's back-edge operand is the phi itself, and the
	// allocator once let a body temporary take its register, so the next
	// iteration compared against garbage (borrowed_forward_lifetime returned
	// 11, the bit sieve counted no primes).
	{name: "invariant_through_phi", src: `
@noinline
function step(x: i32, i: i32): i32 { return x + i; }
function forward(x: i32, rounds: i32): i32 {
    var i: i32 = 0;
    var acc: i32 = x;
    while (i < rounds) { acc = step(acc, i); i = i + 1; }
    return acc;
}
@noinline
function bit(w: i32, i: i32): boolean { return ((w >> (i & 31)) & 1) == 1; }
function count_bits(w: i32, n: i32): i32 {
    var c: i32 = 0;
    for i in 0..(n + 1) { if (bit(w, i)) { c = c + 1; } }
    return c;
}
function main(): i32 { return (forward(3, 10) + count_bits(1431655765, 31) * 10) % 256; }
`},
	// The box layouts: records, arrays, tuples, Option boxes, enum variants,
	// string literals and bytes, each lifted onto the flat backend's layout
	// (ssa.fern kinds 40 to 48) rather than through a runtime call.
	{name: "boxes", src: `
struct P { x: i32, y: i32 }
enum Shape { Dot(i32), Line(i32, i32), Empty }
function mk(a: i32, b: i32): P { return P { x: a, y: b }; }
function sum(p: P): i32 { return p.x + p.y; }
function third(xs: i32[]): i32 { if (xs.len() > 2) { return xs[2]; } return 0 - 1; }
function pair(a: i32): (i32, i32) { return (a, a * 2); }
function unwrap(o: Option[i32]): i32 { match (o) { Some(v) => { return v; }, None => { return 0; } } }
function area(s: Shape): i32 {
    match (s) {
        Dot(r) => { return r; },
        Line(a, b) => { return a * b; },
        Empty => { return 0; },
    }
}
function letters(): i32 { var s: string = "fern"; return s.len() * 100 + (s[1] as i32); }
function main(): i32 {
    var p: P = mk(3, 4);
    var xs: i32[] = [5, 6, 7];
    var t: (i32, i32) = pair(9);
    var total: i32 = sum(p) + third(xs) + t.0 + t.1 + unwrap(Some(11)) + unwrap(None)
        + area(Dot(2)) + area(Line(3, 4)) + area(Empty) + letters();
    return total % 256;
}
`},
	// The runtime calls the stack machine makes for string and array ops: the
	// Fern-compiled helpers on the stack ABI (concat, equality, ordering) and
	// the register-ABI routines (array push).
	{name: "runtime_calls", src: `
function join(a: string, b: string): string { return a + b; }
function same(a: string, b: string): boolean { return a == b; }
function before(a: string, b: string): boolean { return a < b; }
function grow(xs: i32[], v: i32): i32[] { return xs.append(v); }
function mid(s: string, lo: i32, hi: i32): i32 {
    match (s[lo:hi]) {
        Some(t) => { return t.len() * 10 + (t[0] as i32); },
        None => { return 0 - 1; },
    }
}
function main(): i32 {
    var s: string = join("fe", "rn");
    var xs: i32[] = grow([1, 2], 3);
    var n: i32 = xs.len() * 10 + s.len() + xs[2] + mid(s, 1, 3) + mid("abcdef", 2, 5) + mid("ab", 1, 9);
    if (same(s, "fern")) { n = n + 100; }
    if (before("apple", s)) { n = n + 1; }
    return n % 256;
}
`},
	// A loop appending a constant through the owned push: the array and the
	// value both sit in registers at the runtime call, and on x86-64 the
	// argument registers are among the allocatable ones, so the call must
	// read both homes before it writes either.
	{name: "byte_sieve", src: `
function sieve(n: i32): boolean[] {
    var s: boolean[] = [];
    for i in 0..(n + 1) { s = s.append(true); }
    if (n >= 0) { s = s.with(0, false); }
    if (n >= 1) { s = s.with(1, false); }
    var i: i32 = 2;
    while (i * i <= n) {
        if (s[i]) {
            var j: i32 = i * i;
            while (j <= n) { s = s.with(j, false); j = j + i; }
        }
        i = i + 1;
    }
    return s;
}
function count(s: boolean[]): i32 {
    var n: i32 = 0;
    for b in s { if (b) { n = n + 1; } }
    return n;
}
function main(): i32 { return count(sieve(1000)); }
`},
	// std/json's array parser ends its loop body in a return, so the block
	// holding the return reaches the loop's end live and terminated; the lift
	// must append it, or the if before it branches to a label nothing defines.
	{name: "json_array", src: `
import "core/cmp";
import "std/json";
function main(): i32 {
    match (json.json_parse_result("[1, [2, 3], [], {\"k\": [4]}]")) {
        Ok(v) => { return json.json_encode(v).len(); },
        Err(e) => { return 1; },
    }
}
`},
	// f64 throughout: negative literals and -0.0, the arithmetic and the
	// NaN-aware compares, the single-instruction math, every conversion
	// width and signedness including the saturating ones and the widening
	// the lowering inserts for an integer literal at an f64 parameter, the
	// f32 and i64 reinterprets, and the transcendentals the runtime supplies.
	// A value is its bit pattern in an integer register, as on the stack
	// machine.
	{name: "floats", src: `
import "std/float";
function area(r: f64): f64 { return 3.141592653589793 * r * r; }
function hyp(a: f64, b: f64): f64 { return (a * a + b * b).sqrt(); }
function classify(x: f64): i32 {
    if (x < 0.0) { return 0 - 1; }
    if (x == 0.0) { return 0; }
    if (x > 1000.5) { return 2; }
    if (x >= 2.5 && x <= 2.5) { return 3; }
    if (x != x) { return 9; }
    return 1;
}
function rounding(x: f64): i32 {
    var f: i32 = x.floor() as i32;
    var c: i32 = x.ceil() as i32;
    var t: i32 = x.trunc() as i32;
    var r: i32 = x.round() as i32;
    return f + c * 10 + t * 100 + r * 1000;
}
function convs(n: i32, u: u32, w: i64, v: u64): i64 {
    var a: f64 = n as f64;
    var b: f64 = u as f64;
    var c: f64 = w as f64;
    var d: f64 = v as f64;
    var s: f64 = a + b + c + d;
    return (s as i64) + ((s / 3.0) as i32) as i64 + (((0.0 - s) as u32) as i64) + ((s * 1e30) as i32) as i64;
}
function bits(x: f64): i64 {
    var b: i64 = f64_bits(x);
    var y: f64 = f64_from_bits(b + 1);
    var h: i32 = f32_bits(y as f32);
    var zf: f32 = f32_from_bits(h);
    var z: f64 = zf as f64;
    return b + (z as i64) + (h as i64) % 7;
}
function neg(x: f64): f64 { return (0.0 - x).abs() - (0.0 - x); }
function widen(x: f64): f64 { return area(2) + hyp(3, 4) + x; }
function signs(): i32 {
    var m: f64 = -2.5;
    var z: f64 = -0.0;
    var r: i32 = 0;
    if (m < 0.0) { r = r + 1; }
    if (f64_bits(z) != 0) { r = r + 2; }
    if (z == 0.0) { r = r + 4; }
    return r + (m.abs() * 2.0) as i32;
}
function trans(x: f64): i32 { return ((x.sin() * 1000.0) as i32) + ((x.cos() * 1000.0) as i32) + ((x.exp() * 10.0) as i32) + ((x.log() * 1000.0) as i32) + (x.pow(2.5) as i32); }
function main(): i32 {
    var acc: i64 = (area(2.0) * 1000.0) as i64;
    acc = acc + (hyp(3.0, 4.0) as i64);
    acc = acc + (classify(0.0 - 2.5) + classify(0.0) + classify(2000.0) + classify(2.5) + classify(0.0 / 0.0) + classify(7.0)) as i64;
    acc = acc + (rounding(2.5) + rounding(0.0 - 2.5) + rounding(3.7)) as i64;
    acc = acc + convs(0 - 7, 4000000000, 5000000000, 18446744073709551615);
    acc = acc + bits(1.5) + (neg(4.0) as i64);
    acc = acc + trans(1.5) as i64;
    acc = acc + (widen(1.5) * 10.0) as i64 + signs() as i64;
    return (acc % 251) as i32;
}
`},
	// The host floor: env, read_file and stat through their Fern helpers on
	// the stack ABI, whose bodies use the raw syscalls, the scratch buffer,
	// the string box stamp and the width loads; monotonic_ns with no
	// operand; exit as the raw syscall. Whole, so the helper bodies and the
	// byte kernels they use go through the backend on every target,
	// including the Darwin rewrite of the syscall number register.
	{name: "host_calls", src: `
import "std/i32";
function probe_env(): i32 {
    var n: i32 = 0;
    match (env("FERN_SSA_HOST_PROBE_UNSET")) { Some(v) => { n = n + 100; }, None => { n = n + 1; } }
    match (env("PATH")) { Some(v) => { if (v.len() > 0) { n = n + 2; } }, None => { n = n + 200; } }
    return n;
}
function probe_fs(path: string): i32 {
    var n: i32 = 0;
    match (read_file(path)) { Ok(s) => { n = n + 300; }, Err(e) => { n = n + 4; } }
    match (stat("/")) { Ok(st) => { n = n + 8; }, Err(e) => { n = n + 400; } }
    match (stat(path)) { Ok(st) => { n = n + 500; }, Err(e) => { n = n + 16; } }
    return n;
}
function probe_clock(): i32 {
    var a: i64 = monotonic_ns();
    var b: i64 = monotonic_ns();
    if (a > 0 && b >= a) { return 32; }
    return 600;
}
function main(): i32 {
    var n: i32 = probe_env() + probe_fs("/nonexistent/fern/ssa/host/probe") + probe_clock();
    print("host " + n.to_string() + "\n");
    exit(n % 100);
    return 7;
}
`},
	// The OS floor the compiler itself never calls: the process and host
	// queries, umask, and the handle ops (open, fstat, the open flags,
	// isatty, close). None has a selection of its own in the register path;
	// each runs through the stack machine's arm for it (flat_op), which is
	// how every op with a known stack effect reaches this backend.
	{name: "os_floor", src: `
import "std/i32";
function probe_ids(): i32 {
    var n: i32 = 0;
    if (geteuid() < 1000000000) { n = n + 1; }
    if (cpu_count() > 0) { n = n + 2; }
    if (hostname().len() > 0) { n = n + 4; }
    if (getcwd().len() > 0) { n = n + 8; }
    if (uname_field(0).len() > 0) { n = n + 16; }
    var old: i32 = umask(18);
    if (umask(old) == 18) { n = n + 32; }
    return n;
}
function probe_handle(path: string): i32 {
    var n: i32 = 0;
    match (open_reader(path)) {
        Ok(r) => {
            match (r.stat()) { Ok(st) => { n = n + 100; }, Err(e) => { n = n + 1000; } }
            match (r.flags()) { Ok(f) => { n = n + 200; }, Err(e) => { n = n + 2000; } }
            if (!r.isatty()) { n = n + 400; }
            match (r.close()) { Some(e) => { n = n + 4000; }, None => { n = n + 800; } }
        },
        Err(e) => { n = n + 8000; },
    }
    return n;
}
function main(): i32 {
    var n: i32 = probe_ids() + probe_handle("/dev/null") + probe_handle("/nonexistent/fern/ssa/os/floor");
    print("os " + n.to_string() + "\n");
    return n % 100;
}
`},
	// `dyn Trait` dispatch: the receiver's shape word selects the arm, and
	// the arguments are read from wherever the allocator put them, a
	// register or a spill slot, rather than from the locals the stack
	// machine's arm reads. A struct, an enum (matched per variant) and a
	// primitive (unboxed at the call) receiver, a two-argument method, and
	// an argument a call defines at the instruction before the dispatch, so
	// its home is the return register the chain uses as scratch
	// (TestSelfHostSSADynDispatchArgumentInScratch pins that it is).
	{name: "dyn_dispatch", src: `
import "std/i32";
trait Shape {
    function area(self: Self): i32;
    function scaled(self: Self, k: i32): i32;
}
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function scaled(self: Self, k: i32): i32 { return self.side * k; }
}
enum Blob { Round(i32), Flat }
impl Shape for Blob {
    function area(self: Self): i32 { match (self) { Round(r) => { return r * 3; }, Flat => { return 0; } } }
    function scaled(self: Self, k: i32): i32 { return self.area() * k; }
}
impl Shape for i32 {
    function area(self: Self): i32 { return self; }
    function scaled(self: Self, k: i32): i32 { return self * k; }
}
function measure(s: dyn Shape, k: i32): i32 { return s.area() * 100 + s.scaled(k); }
@noinline
function bump(n: i32): i32 { return n + 1; }
function arg_from_call(s: dyn Shape, n: i32): i32 { return s.scaled(bump(n)); }
function main(): i32 {
    var a: dyn Shape = Square { side: 3 };
    var b: dyn Shape = Round(5);
    var c: dyn Shape = Flat;
    var d: dyn Shape = 7;
    var total: i32 = measure(a, 2) + measure(b, 3) + measure(c, 4) + measure(d, 5) + arg_from_call(a, 1);
    print("dyn " + total.to_string() + "\n");
    return total % 256;
}
`},
	// The map ops and the byte kernels run through the stack machine's own
	// arms between a push of the operands and a pop of the result, with the
	// allocator treating each as a call. String and integer keys, insert,
	// lookup with a default, membership, the key and value snapshots.
	{name: "maps", src: `
import "core/map";
import "std/i32";
function build(n: i32): Map[string, i32] {
    var m: Map[string, i32] = Map { };
    var i: i32 = 0;
    while (i < n) { m = m.insert("k" + i.to_string(), i * 3); i = i + 1; }
    return m;
}
function main(): i32 {
    var m: Map[string, i32] = build(50);
    var ints: Map[i32, i32] = Map { };
    var j: i32 = 0;
    while (j < 40) { ints = ints.insert(j * 7, j); j = j + 1; }
    var total: i32 = m.get_or("k7", 0) + m.get_or("zz", 100) + ints.get_or(21, 0) + ints.get_or(22, 1000);
    if (m.has("k9")) { total = total + 1; }
    if (!ints.has(5)) { total = total + 2; }
    total = total + m.len() * 10 + ints.keys().len() + ints.values().len();
    for k in m.keys() { if (k.len() == 2) { total = total + 1; } }
    return total % 200;
}
`},
	// Aggregates the lowering folds whole (a record literal with constant
	// fields, here the keys of a persistent map) are the address of an
	// interned box; passed straight to a call, that address sits in a
	// register home and must survive there. The map's own functions are
	// pinned with the program: the lookup on a collision node is where a
	// clobbered key address surfaced.
	{name: "folded_aggregates", src: `
import "std/pmap" as pmap;
import "std/option";
import "std/i32";
import "core/cmp";

@derive(cmp.Eq)
struct Coarse { bucket: i32, id: i32 }
impl cmp.Hash for Coarse { function hash(self: Coarse): i32 { return self.bucket; } }
function main(): i32 {
    var m: pmap.PMap[Coarse, i32] = pmap.pmap_new();
    var i: i32 = 0;
    while (i < 40) { m = m.insert(Coarse { bucket: i % 5, id: i }, i); i = i + 1; }
    var n: i32 = m.get_or(Coarse { bucket: 3, id: 13 }, -1) * 10 + m.get_or(Coarse { bucket: 3, id: 14 }, -1);
    if (m.contains(Coarse { bucket: 1, id: 6 })) { n = n + 1000; }
    return n % 251;
}
`},
	// The string ops the stack machine selects in emit_str_op (search, split,
	// lines, case, trim, replace, bytes, count), through the stack machine's
	// own arm with the operand count the IR verifier gives each, and the bit
	// counts at both widths.
	{name: "strings", src: `
import "std/string";
import "std/i32";
import "std/i64";
function strs(s: string): i32 {
    var n: i32 = 0;
    n = n + s.index_of("fern");
    if (s.starts_with("the")) { n = n + 100; }
    if (s.ends_with("end")) { n = n + 200; }
    if (s.contains("language")) { n = n + 400; }
    n = n + s.split(" ").len() * 10;
    n = n + s.lines().len() * 1000;
    n = n + s.trim().len();
    n = n + s.replace("fern", "FERN").to_upper().len() + s.to_lower().len();
    n = n + "ab".repeat(3).len() + s.reverse_bytes().len();
    n = n + s.bytes().len() + s.count("e");
    return n;
}
function bits(a: i32, b: i64): i32 {
    return a.count_ones() + a.leading_zeros() * 10 + a.trailing_zeros() * 100 + (b.count_ones() as i32) * 1000 + (b.leading_zeros() as i32) * 7 + (b.trailing_zeros() as i32) * 3;
}
function main(): i32 {
    var s: string = "  the fern language\n has a fern end";
    return (strs(s) + bits(15790080, 280375465082880)) % 251;
}
`},
	// A string slice whose upper bound is read again in the same block. The
	// slice kernel loads that bound into the scratch register and then
	// overwrites it with the length, so a scratch tracker that survives the
	// kernel makes the second read reuse the difference. With lo of 0 the
	// wrong value equals the right one, so a case has to slice from further
	// in to see it.
	{name: "slice_bound_reuse", src: `
function tail(s: string, lo: i32, hi: i32): i32 {
    var v: str = slice_unchecked(s, lo, hi);
    var x: i32 = hi + 1;
    return x * 1000 + v.len();
}
function main(): i32 {
    var s: string = "abcdefghijkl";
    return (tail(s, 3, 9) + tail(s, 0, 4) + tail(s, 5, 12)) % 251;
}
`},
	// An exhaustive match whose every arm returns, standing last in a
	// generic function (ordmap's fold), lowers to a loop whose body reaches
	// the loop's end alive: control continues after the loop in the
	// enclosing scope, and the lift gives it a block to continue in.
	{name: "loop_fallthrough", src: `
import "std/ordmap" as ordmap;
import "core/cmp";
function main(): i32 {
    var m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
    var i: i32 = 0;
    while (i < 30) { m = m.insert((i * 7) % 31, i); i = i + 1; }
    var total: i32 = m.fold(0, (acc: i32, k: i32, v: i32) => acc + k * 2 + v);
    return (total + m.len()) % 251;
}
`},
	// Integer functions beside string-heavy ones, calling each other through
	// the shared stack ABI: the shape a module had when the register path
	// still left the string helpers to the stack machine.
	{name: "mixed_module", src: `
import "std/i32";
function sum_to(n: i32): i32 { var s: i32 = 0; var i: i32 = 1; while (i <= n) { s = s + i; i = i + 1; } return s; }
function gcd(a: i32, b: i32): i32 { while (b != 0) { var t: i32 = b; b = a % b; a = t; } return a; }
function eight(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a - b + c - d + e - f + g - h; }
function main(): i32 {
    print(sum_to(100).to_string());
    print(gcd(1071, 462).to_string());
    print(eight(1, 2, 3, 4, 5, 6, 7, 8).to_string());
    var xs: i32[] = [3, 1, 2];
    print((xs.len() + gcd(xs[0], xs[2])).to_string());
    return 3;
}
`},
	// A constant every use of which is a binary op's operand is an immediate
	// and never materialised: on either side of the commutative ops and the
	// compares, on the right of a subtraction, in a branch's fused compare,
	// negative, at i32 wrap, and for arm64 on either side of its 12-bit
	// range. A constant a shift reads, or one above imm32, keeps its
	// register; one a division reads keeps its register on arm64 and feeds
	// the literal-divisor forms on x86-64.
	{name: "immediates", src: `
import "std/i32";
function mix(x: i32, y: i64, u: u32): i32 {
    var a: i32 = x + 7;
    var b: i32 = 7 - x;
    var c: i32 = x - 7;
    var d: i32 = 3 * x;
    var e: i32 = x & 255;
    var f: i32 = 4096 | x;
    var g: i32 = x ^ -1;
    var h: i32 = x * 1000003;
    var w: i32 = x + 2147483647;
    var k: i32 = 0;
    if (x < 10) { k = k + 1; }
    if (10 < x) { k = k + 2; }
    if (x == -1) { k = k + 4; }
    if (-1 != x) { k = k + 8; }
    if (y >= 3000000i64) { k = k + 16; }
    if (3000000i64 > y) { k = k + 32; }
    if (u < 4000000000u32) { k = k + 64; }
    if (u > 100u32) { k = k + 128; }
    if (5000i64 - y < 0i64) { k = k + 256; }
    var m: i64 = (y + 5000i64) * 3i64 - (y % 7i64) + (y >> 2i64);
    var q: i32 = a + b + c + d + e + f + g + h + w + k + (x % 7) + (x / 3) + (m as i32);
    var i: i32 = 0;
    while (i < 3000) { q = q + 1; i = i + 1; }
    return q;
}
function main(): i32 {
    print(mix(5, 10i64, 3u32).to_string());
    print(mix(-1, 3000001i64, 4000000000u32).to_string());
    print(mix(2147483647, -5000i64, 200u32).to_string());
    print(mix(11, 5000i64, 100u32).to_string());
    return mix(0, 0i64, 0u32) % 100;
}
`},
	// The loop-carried phi pairs the allocator coalesces, in every shape
	// where sharing one register would be wrong: the phi's old value read
	// after its operand is defined, on a latch exit and inside a nested
	// loop; a swap of two carried values; several back edges into one
	// header; an exit that reads both; and an update the two-operand forms
	// cannot compute in place.
	{name: "carried_pairs", src: `
import "std/i32";
function old_after_latch(n: i32): i32 {
    var sum: i32 = 0;
    var i: i32 = 0;
    var old: i32 = 0;
    while (true) {
        old = sum;
        sum = sum + i * 3;
        i = i + 1;
        if (i > n) { break; }
    }
    return old * 1000 + sum;
}
function swap(n: i32): i32 {
    var a: i32 = 1;
    var b: i32 = 2;
    var i: i32 = 0;
    while (i < n) { var t: i32 = a; a = b; b = t; i = i + 1; }
    return a * 10 + b;
}
function inner_reads_outer(n: i32): i32 {
    var acc: i32 = 1;
    var o: i32 = 0;
    while (o < n) {
        var k: i32 = 0;
        var step: i32 = 0;
        while (k < 3) { step = step + acc + k; k = k + 1; }
        acc = step;
        o = o + 1;
    }
    return acc;
}
function two_latches(n: i32): i32 {
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        i = i + 1;
        if (i % 3 == 0) { s = s + 100; continue; }
        s = s + i;
    }
    return s;
}
function exit_reads_both(n: i32): i32 {
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var next: i32 = s + i;
        if (next > 50) { return next * 7 + s; }
        s = next;
        i = i + 1;
    }
    return s;
}
function shifted(n: i32): i32 {
    var x: i32 = 1;
    var i: i32 = 0;
    while (i < n) { x = ((x << 1) | 1) % 1000003; x = x / 3 + x; i = i + 1; }
    return x;
}
function main(): i32 {
    print(old_after_latch(5).to_string());
    print(swap(3).to_string());
    print(swap(4).to_string());
    print(inner_reads_outer(4).to_string());
    print(two_latches(10).to_string());
    print(exit_reads_both(20).to_string());
    print(exit_reads_both(5).to_string());
    print(shifted(40).to_string());
    return two_latches(7) % 100;
}
`},
	// Blocks that hold nothing but a branch: empty arms, a dead arm, a loop
	// never entered, a body with nothing live. The emitter sends every edge
	// past them and drops the ones nothing reaches; the phis of the loops
	// keep their edges.
	{name: "empty_blocks", src: `
function empties(n: i32): i32 {
    var k: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        if (i % 2 == 0) { } else { }
        if (i > 3) { var dead: i32 = i * 9; }
        while (k > 100) { k = k - 1000; }
        k = k + i;
        i = i + 1;
    }
    var j: i32 = 0;
    while (j < n) { var unused: i32 = j * 2; j = j + 1; }
    if (n > 2) { } else { k = k + 7; }
    return k + j;
}
function main(): i32 { return empties(6) * 3 + empties(1); }
`},
}

// ssaBackendTarget is one target this host can run output for: the target
// name and the runner prefix for a binary built for it ("" when native).
type ssaBackendTarget struct {
	target string
	runner []string
}

// ssaBackendHost is the self-host CLI built for this host, the native
// compiler it was built with (the differential's oracle), the targets whose
// output this host can run, and the stdlib root.
type ssaBackendHost struct {
	cli     string
	native  string
	targets []ssaBackendTarget
	stdlib  string
}

var (
	ssaHostOnce sync.Once
	ssaHost     ssaBackendHost
	ssaHostSkip string
	ssaHostErr  string
)

// hostTargets lists the targets this host can run output for: the native
// one, plus the other ISA through its qemu user emulator when that is on PATH.
// The CLI itself is built for the host and never run under an emulator, since
// it takes host filesystem paths.
func hostTargets() (cliTarget string, targets []ssaBackendTarget, skip string) {
	emulated := func(target, qemu string) {
		if q, err := exec.LookPath(qemu); err == nil {
			targets = append(targets, ssaBackendTarget{target: target, runner: []string{q}})
		}
	}
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		cliTarget = "arm64-darwin"
		targets = append(targets, ssaBackendTarget{target: "arm64-darwin"})
		emulated("x86-64-linux", "qemu-x86_64")
	case runtime.GOOS == "linux" && runtime.GOARCH == "arm64":
		cliTarget = "arm64-linux"
		targets = append(targets, ssaBackendTarget{target: "arm64-linux"})
		emulated("x86-64-linux", "qemu-x86_64")
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		cliTarget = "x86-64-linux"
		targets = append(targets, ssaBackendTarget{target: "x86-64-linux"})
		emulated("arm64-linux", "qemu-aarch64")
	default:
		skip = runtime.GOOS + "/" + runtime.GOARCH + " cannot run the self-host CLI or its output"
	}
	return
}

// selfHostCLIForHost builds examples/self_host/fern.fern once per test binary
// as a binary this host executes directly.
func selfHostCLIForHost(t *testing.T) ssaBackendHost {
	t.Helper()
	ssaHostOnce.Do(func() {
		cliTarget, targets, skip := hostTargets()
		if skip != "" {
			ssaHostSkip = skip
			return
		}
		fern := buildLangBinForInterp(t)
		src, err := filepath.Abs("../../examples/self_host/fern.fern")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		stdlib, err := filepath.Abs("../../internal/stdlib")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		dir, err := os.MkdirTemp("", "selfhost-ssa-cli-")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		cli := filepath.Join(dir, "fern")
		if out, err := exec.Command(fern, "-target", cliTarget, "-o", cli, src).CombinedOutput(); err != nil {
			// Recorded rather than fatal here: sync.Once runs this for the
			// FIRST test only, so failing inside it left every later test with
			// a zero-valued host and no skip reason — they indexed an empty
			// targets slice and panicked, which reads as a test bug rather
			// than as the build failure it is.
			ssaHostErr = fmt.Sprintf("building the self-host CLI for %s: %v\n%s", cliTarget, err, out)
			return
		}
		ssaHost = ssaBackendHost{cli: cli, native: fern, targets: targets, stdlib: stdlib}
	})
	if ssaHostErr != "" {
		t.Fatal(ssaHostErr)
	}
	if ssaHostSkip != "" {
		t.Skip(ssaHostSkip)
	}
	return ssaHost
}

// compileWith runs the CLI on src for the target, with the extra flags.
func (h ssaBackendHost) compileWith(t *testing.T, tg ssaBackendTarget, src, out string, extra ...string) {
	t.Helper()
	args := append([]string{"-target", tg.target}, extra...)
	args = append(args, "-o", out, src, h.stdlib)
	if out, err := exec.Command(h.cli, args...).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
	}
}

// compileNative runs the native compiler on src for the target.
func (h ssaBackendHost) compileNative(t *testing.T, tg ssaBackendTarget, src, out string) {
	t.Helper()
	args := []string{"-target", tg.target, "-o", out, src}
	if out, err := exec.Command(h.native, args...).CombinedOutput(); err != nil {
		t.Fatalf("native fern %v: %v\n%s", args, err, out)
	}
}

// runProduced runs a binary the CLI produced and returns its stdout and exit
// status. A program that does not exit normally within the timeout fails.
func (h ssaBackendHost) runProduced(t *testing.T, tg ssaBackendTarget, bin string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if len(tg.runner) == 0 {
		cmd = exec.CommandContext(ctx, bin)
	} else {
		cmd = exec.CommandContext(ctx, tg.runner[0], append(tg.runner[1:], bin)...)
	}
	var stdout strings.Builder
	cmd.Stdout = &stdout
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally: %v", bin, cmd.ProcessState)
	}
	return stdout.String(), cmd.ProcessState.ExitCode()
}

// The oracle is the native compiler's build of the same program: the stack
// machine that used to stand on the other side of this differential is gone,
// and native's emitters are the reference the self-host converges on
// (docs/NATIVE-CONVERGENCE.md).
func TestSelfHostSSABackendAgreesWithNative(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, tg := range h.targets {
		tg := tg
		for _, p := range ssaBackendPrograms {
			p := p
			t.Run(tg.target+"/"+p.name, func(t *testing.T) {
				dir := t.TempDir()
				src := filepath.Join(dir, p.name+".fern")
				if err := os.WriteFile(src, []byte(p.src), 0o644); err != nil {
					t.Fatal(err)
				}
				native := filepath.Join(dir, p.name+".native")
				ssa := filepath.Join(dir, p.name+".ssa")
				h.compileNative(t, tg, src, native)
				h.compileWith(t, tg, src, ssa)

				nativeOut, nativeExit := h.runProduced(t, tg, native)
				ssaOut, ssaExit := h.runProduced(t, tg, ssa)
				if nativeExit != ssaExit {
					t.Errorf("%s: exit %d through native, %d through the self-host register path", p.name, nativeExit, ssaExit)
				}
				if nativeOut != ssaOut {
					t.Errorf("%s: stdout differs\n--- native\n%s\n--- self-host register path\n%s", p.name, nativeOut, ssaOut)
				}
			})
		}
	}
}

// Each target has one emitter and `-backend` may only name it: `ssa` on the
// two native ISAs, refused for wasm with the targets it is available for, as
// native's `-backend ssa` refuses wasm; `flat` on wasm, refused on the native
// ISAs now that their stack machine is gone; and a name nothing implements is
// an error rather than a fall-through.
func TestSelfHostSSABackendRefusesOtherTargets(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-target", "wasm32-wasi", "-backend", "ssa", "-o", filepath.Join(dir, "p"), src, h.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("-backend ssa for wasm32-wasi was accepted")
	}
	if !strings.Contains(string(out), "-backend ssa is not available for -target wasm32-wasi") {
		t.Errorf("refusal does not name the target: %s", out)
	}
	out, err = exec.Command(h.cli, "-target", h.targets[0].target, "-backend", "nope", "-o", filepath.Join(dir, "p"), src, h.stdlib).CombinedOutput()
	if err == nil {
		t.Fatalf("-backend nope was accepted")
	}
	if !strings.Contains(string(out), "unknown -backend: nope") {
		t.Errorf("unknown backend not reported: %s", out)
	}
	// Omitting -backend selects the target's emitter, and naming it must
	// reproduce that byte for byte. h.targets is native-only, so the loop
	// covers the register path; wasm is below.
	for _, tg := range h.targets {
		base := strings.ReplaceAll(tg.target, "-", "_")
		h.compileWith(t, tg, src, filepath.Join(dir, base+"_dflt"))
		h.compileWith(t, tg, src, filepath.Join(dir, base+"_ssa"), "-backend", "ssa")
		dflt, err := os.ReadFile(filepath.Join(dir, base+"_dflt"))
		if err != nil {
			t.Fatal(err)
		}
		named, err := os.ReadFile(filepath.Join(dir, base+"_ssa"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dflt, named) {
			t.Errorf("%s: -backend ssa differs from the default emitter, which it is", tg.target)
		}
		// The stack machine's function emitter is gone from the native ISAs,
		// so the name is refused rather than mapped onto the register path: a
		// script asking for the stack machine by name would otherwise compare
		// the register path with itself.
		out, err := exec.Command(h.cli, "-target", tg.target, "-backend", "flat", "-o", filepath.Join(dir, base+"_flat"), src, h.stdlib).CombinedOutput()
		if err == nil {
			t.Fatalf("%s: -backend flat was accepted", tg.target)
		}
		if !strings.Contains(string(out), "-backend flat is not available for -target "+tg.target) {
			t.Errorf("%s: refusal does not name the target: %s", tg.target, out)
		}
	}
	// wasm has no register path, so the stack machine is its emitter and
	// `flat` names it. Without this the rule is only half-pinned: the refusal
	// above says `-backend ssa` is unavailable for wasm, not that omitting it
	// lands on flat.
	wasmDflt := filepath.Join(dir, "wasm_dflt")
	wasmFlat := filepath.Join(dir, "wasm_flat")
	for _, a := range [][]string{{"-o", wasmDflt}, {"-backend", "flat", "-o", wasmFlat}} {
		args := append([]string{"-target", "wasm32-wasi"}, a...)
		if out, err := exec.Command(h.cli, append(args, src, h.stdlib)...).CombinedOutput(); err != nil {
			t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
		}
	}
	dflt, err := os.ReadFile(wasmDflt)
	if err != nil {
		t.Fatal(err)
	}
	flat, err := os.ReadFile(wasmFlat)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dflt, flat) {
		t.Errorf("wasm32-wasi: -backend flat differs from the default emitter, which it is")
	}
}

// A second `-o` to the same path replaces the executable rather than
// rewriting it in place. macOS caches the code-signature verdict of an
// executable by inode, so an in-place rewrite is killed at exec with "Code
// Signature Invalid" while a byte-identical copy at a fresh path runs. The
// portable observation is a handle held open across the second compile: a
// replaced file leaves it reading the first program, an in-place rewrite
// shows it the second. Apple Silicon also runs the second program, which is
// the exec the cache would have killed.
// The lift gives every merge a phi per local slot and prune_dead removes
// almost all of them, so the id space of a large function is hundreds of
// times its live values. The frame is sized by the spilled values, not by
// the ids: a function with 200 locals and 200 merges reserves well under
// 16 KB on both ISAs, where a slot per id would be over 300 KB.
func TestSelfHostSSAFrameIsSizedBySpills(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("function wide(n: i32): i32 {\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    var v%d: i32 = n + %d;\n", i, i)
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    if (n > %d) { v%d = v%d + 1; }\n", i, i, (i+1)%200)
	}
	b.WriteString("    var s: i32 = 0;\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    s = s + v%d;\n", i)
	}
	b.WriteString("    return s;\n}\nfunction main(): i32 { return wide(3) % 256; }\n")
	src := filepath.Join(dir, "wide.fern")
	if err := os.WriteFile(src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		frame  *regexp.Regexp
	}{
		{"x86-64-linux", regexp.MustCompile(`__fn_wide:\n(?:.*\n){1,14}?\s+subq \$(\d+), %rsp`)},
		// Over 4,095 bytes the arm64 prologue builds the immediate in x17; a
		// movk after the movz would mean a frame over 64 KB. The window covers
		// the frame record and up to five callee-saved pairs before it.
		{"arm64-linux", regexp.MustCompile(`__fn_wide:\n(?:.*\n){1,16}?\s+(?:sub sp, sp, #|movz x17, #)(\d+)\n(\s+movk)?`)},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "wide-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", out, src, h.stdlib)
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		m := c.frame.FindSubmatch(asm)
		if m == nil {
			t.Fatalf("%s: no frame reservation found after __fn_wide", c.target)
		}
		n, _ := strconv.Atoi(string(m[1]))
		if len(m) > 2 && len(m[2]) > 0 {
			n += 64 * 1024
		}
		if n > 16*1024 {
			t.Errorf("%s: wide reserves %d bytes of frame; a slot per spilled value stays under 16 KB", c.target, n)
		}
	}
}

func TestSelfHostOutputReplacesExecutable(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.fern")
	second := filepath.Join(dir, "second.fern")
	if err := os.WriteFile(first, []byte("function main(): i32 { return 20; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("function main(): i32 { return 55; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tg := h.targets[0]
	out := filepath.Join(dir, "prog")
	h.compileWith(t, tg, first, out)
	firstBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, exit := h.runProduced(t, tg, out); exit != 20 {
		t.Fatalf("first program exit %d, want 20", exit)
	}
	held, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	h.compileWith(t, tg, second, out)
	stillFirst, err := io.ReadAll(held)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stillFirst, firstBytes) {
		t.Fatalf("the handle opened before the second compile no longer reads the first program; the executable was rewritten in place, not replaced")
	}
	if _, exit := h.runProduced(t, tg, out); exit != 55 {
		t.Fatalf("second program exit %d, want 55", exit)
	}
}

// A path that is not a regular file is written through and kept, the rule
// native's writeExecutable states: `-o /dev/null` used to delete the null
// device and leave the program in its place (#10034). A FIFO stands in for
// the device and must keep its mode; a symlink stays a link and its target
// becomes the executable.
func TestSelfHostOutputKeepsANonRegularPath(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 31; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tg := h.targets[0]

	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o622); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	got := make(chan int, 1)
	go func() {
		f, err := os.Open(fifo)
		if err != nil {
			got <- -1
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		got <- len(b)
	}()
	h.compileWith(t, tg, src, fifo)
	select {
	case n := <-got:
		if n <= 0 {
			t.Fatalf("the FIFO's reader got %d bytes, want the program written through it", n)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the FIFO's reader never saw the write; the path was replaced")
	}
	fi, err := os.Lstat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the FIFO is now %v, want it kept", fi.Mode())
	}
	if perm := fi.Mode().Perm(); perm&0o111 != 0 {
		t.Fatalf("the FIFO's mode became %v; a non-regular path must not be chmodded", perm)
	}

	target := filepath.Join(dir, "prog")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	h.compileWith(t, tg, src, link)
	if li, err := os.Lstat(link); err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link is now %v (%v), want it kept", li, err)
	}
	if _, exit := h.runProduced(t, tg, target); exit != 31 {
		t.Fatalf("the link's target exit %d, want 31", exit)
	}

	// A dangling link creates its target, executable, as native and cp do.
	created := filepath.Join(dir, "created")
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(created, dangling); err != nil {
		t.Fatal(err)
	}
	h.compileWith(t, tg, src, dangling)
	if li, err := os.Lstat(dangling); err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the dangling link is now %v (%v), want it kept", li, err)
	}
	if _, exit := h.runProduced(t, tg, created); exit != 31 {
		t.Fatalf("the dangling link's created target exit %d, want 31", exit)
	}
}

// TestSelfHostCLIBuildsForEveryNativeTarget builds the self-host compiler for
// both native targets on whatever host runs it.
//
// The build is a cross-compile, so the host does not decide what can be built —
// but every other gate here takes its target from the host (hostTargets), which
// means each machine tests one of the two and neither machine tests both. That
// is how #9525 reached main: the arm64 default flip left four runtime helpers
// with no emitter, and `fern -target arm64-linux examples/self_host/fern.fern`
// failed to link on every push while the x86-64 lanes stayed green.
//
// The whole compiler is the point: it is the largest program in the tree and
// the one that reaches the widest set of runtime helpers, so a helper missing
// from a backend shows up here and almost nowhere else. `-o` is passed so the
// link runs too; emit alone would miss a symbol the assembler resolves.
func TestSelfHostCLIBuildsForEveryNativeTarget(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src, err := filepath.Abs("../../examples/self_host/fern.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "fern")
			if o, err := exec.Command(fern, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("the self-host compiler does not build for %s: %v\n%s", target, err, o)
			}
			st, err := os.Stat(out)
			if err != nil {
				t.Fatalf("no binary written for %s: %v", target, err)
			}
			if st.Size() == 0 {
				t.Fatalf("the %s build wrote an empty binary", target)
			}
		})
	}
}

// A counted loop's two loop-carried values share registers with their
// updates and the loop is rotated, so the loop is the two adds, the compare
// and the conditional back edge on both ISAs: no copy in the loop, and no
// jump but the back edge.
func TestSelfHostSSALoopIsFourInstructions(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "count.fern")
	prog := `function count(n: i64): i64 {
    var sum: i64 = 0i64;
    var i: i64 = 0i64;
    while (i < n) { sum = sum + i; i = i + 1i64; }
    return sum;
}
function main(): i32 { return (count(3000i64) % 97i64) as i32; }
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		back   *regexp.Regexp
		move   *regexp.Regexp
	}{
		{"x86-64-linux", regexp.MustCompile(`(?m)^\s+j\w+ (\.Lssa_count_\d+)\n`), regexp.MustCompile(`(?m)^\s+movq %r\w+, %r\w+$`)},
		{"arm64-linux", regexp.MustCompile(`(?m)^\s+b\.?\w* (\.Lssa_count_\d+)\n`), regexp.MustCompile(`(?m)^\s+mov x\d+, x\d+$`)},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "count-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", out, src, h.stdlib)
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_count")
		if fn == "" {
			t.Fatalf("%s: no __fn_count in the listing:\n%s", c.target, asm)
		}
		// The back edge is the last branch to a label defined above it.
		head, last := -1, []int(nil)
		for _, m := range c.back.FindAllStringSubmatchIndex(fn, -1) {
			if at := strings.Index(fn, "\n"+fn[m[2]:m[3]]+":\n"); at >= 0 && at < m[0] {
				head, last = at, m
			}
		}
		if last == nil {
			t.Fatalf("%s: no back edge in count:\n%s", c.target, fn)
		}
		loop := fn[head:last[1]]
		var insts []string
		for _, line := range strings.Split(loop, "\n") {
			if strings.HasPrefix(line, "    ") && !strings.HasPrefix(strings.TrimSpace(line), ".") {
				insts = append(insts, strings.TrimSpace(line))
			}
		}
		if len(insts) != 4 {
			t.Errorf("%s: the loop of count is %d instructions, want 4:\n%s", c.target, len(insts), loop)
		}
		if c.move.MatchString(loop) {
			t.Errorf("%s: the loop of count still copies between registers:\n%s", c.target, loop)
		}
	}
}

// A result takes the register of an operand that dies at its definition,
// and a branch whose false target is the next block falls through into it:
// step's add lands in the product's register on both ISAs (b lives on in
// both arms, so the subtraction still copies it first on x86-64, where sub
// has two operands), and first_over's conditional break is one conditional
// jump out of the loop, so the loop's only unconditional jump is its back
// edge, where the break's false edge was a jump around a jump.
func TestSelfHostSSAResultTakesDyingOperandRegister(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "step.fern")
	prog := `function step(x: i64, y: i64): i64 {
    var a: i64 = x * 3i64;
    var b: i64 = a + y;
    if (b > 100i64) { return b - 7i64; }
    return b;
}
function first_over(n: i64): i64 {
    var i: i64 = 0i64;
    loop { if (i * i > n) { break; } i = i + 1i64; }
    return i;
}
function main(): i32 { return (step(40i64, 2i64) + step(1i64, 2i64) + first_over(50i64)) as i32; }
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target  string
		product *regexp.Regexp // the multiply; group 1 is its destination
		add     string         // the add into that destination, with %s for it
		jump    *regexp.Regexp // an unconditional jump
	}{
		{"x86-64-linux", regexp.MustCompile(`(?m)^\s+imulq \$3, %r\w+, (%r\w+)$`), `(?m)^\s+addq %%r\w+, %s$`, regexp.MustCompile(`(?m)^\s+jmp `)},
		{"arm64-linux", regexp.MustCompile(`(?m)^\s+mul (x\d+), x\d+, x\d+$`), `(?m)^\s+add %[1]s, %[1]s, x\d+$`, regexp.MustCompile(`(?m)^\s+b \.`)},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "step-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", out, src, h.stdlib)
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_step")
		loop := functionListing(string(asm), "__fn_first_over")
		if fn == "" || loop == "" {
			t.Fatalf("%s: step or first_over is missing from the listing:\n%s", c.target, asm)
		}
		m := c.product.FindStringSubmatch(fn)
		if m == nil {
			t.Fatalf("%s: no multiply in step:\n%s", c.target, fn)
		}
		add := regexp.MustCompile(fmt.Sprintf(c.add, regexp.QuoteMeta(m[1])))
		if !add.MatchString(fn) {
			t.Errorf("%s: step's add does not land in the product's register %s:\n%s", c.target, m[1], fn)
		}
		if n := len(c.jump.FindAllString(loop, -1)); n != 1 {
			t.Errorf("%s: first_over has %d unconditional jumps, want the back edge alone:\n%s", c.target, n, loop)
		}
	}
}

// The two rc primitives whose body is a guard chain over the count word are
// rendered at their call sites, not called: put tests uniqueness twice and
// retains once, so its listing carries three guard chains and calls neither
// stub. `__fern_rc_dec` is not one of them — in the self-host it maps to the
// array release, which frees — so the slice call put also makes stays.
func TestSelfHostSSARcPrimitivesAreInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "blk.fern")
	prog := `struct Blk { buf: i32[], note: string, n: i32 }
function (b: Blk) put(i: i32, v: i32): Blk { return Blk { ...b, buf: b.buf.with(i, v) }; }
function main(): i32 {
    var b: Blk = Blk { buf: [0, 0, 0, 0], note: "a refcounted field the update carries", n: 0 };
    var i: i32 = 0;
    while (i < 4) { b = b.put(i, i + 1); i = i + 1; }
    return b.buf[3];
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		floor  *regexp.Regexp // the heap-floor test that opens each chain
		count  *regexp.Regexp // the read of the count word, loaded or compared in place
		absent []string       // the stub calls the chains replace
		poison *regexp.Regexp // this backend's spelling of the sanitizer check
	}{
		{"x86-64-linux",
			regexp.MustCompile(`(?m)^\s+cmpq \$0x10000, %r\w+$`),
			regexp.MustCompile(`(?m)^\s+(?:movl -8\(%r\w+\), %e\w+|cmpl \$[01], -8\(%r\w+\))$`),
			[]string{"call __fn___fern_rc_is_unique", "call __fn___fern_rc_inc"},
			regexp.MustCompile(`cmpl \$` + rcPoisonWord + `, %e\w+`)},
		{"arm64-linux",
			regexp.MustCompile(`(?m)^\s+cmp x\d+, #16, lsl #12$`),
			regexp.MustCompile(`(?m)^\s+ldur w5, \[x\d+, #-8\]$`),
			[]string{"bl __fn___fern_rc_is_unique", "bl __fn___fern_rc_inc"},
			regexp.MustCompile(fmt.Sprintf(`movz w\d+, #%d\n\s+movk w\d+, #%d, lsl #16`,
				ast.RcPoison&0xffff, (ast.RcPoison>>16)&0xffff))},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "blk-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", out, src, h.stdlib)
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_Blk__put")
		if fn == "" {
			t.Fatalf("%s: no __fn_Blk__put in the listing:\n%s", c.target, asm)
		}
		if n := len(c.floor.FindAllString(fn, -1)); n < 3 {
			t.Errorf("%s: put has %d inline rc chains, want its two uniqueness tests and its retain:\n%s", c.target, n, fn)
		}
		if !c.count.MatchString(fn) {
			t.Errorf("%s: put reads no count word:\n%s", c.target, fn)
		}
		for _, call := range c.absent {
			if strings.Contains(fn, call) {
				t.Errorf("%s: put still calls the stub (%s):\n%s", c.target, call, fn)
			}
		}
		// Each rc stub reads the count through the sanitizer's poison check, so
		// every inline form must too, or a use after free goes uncaught at the
		// sites the calls were replaced at. That is one check per chain — both
		// uniqueness tests and the retain — not merely one somewhere in the
		// function (#9889). The spelling differs because arm64 needs two halves
		// to materialise ast.RcPoison and compares registers rather than an
		// immediate.
		poison := filepath.Join(dir, "blk-poison-"+c.target+".s")
		pc := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", poison, src, h.stdlib)
		pc.Env = append(os.Environ(), "FERN_RC_FREE_DEBUG=1")
		if out, err := pc.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, out)
		}
		pasm, err := os.ReadFile(poison)
		if err != nil {
			t.Fatal(err)
		}
		pfn := functionListing(string(pasm), "__fn_Blk__put")
		chains := len(c.floor.FindAllString(pfn, -1))
		if got := len(c.poison.FindAllString(pfn, -1)); got != chains {
			t.Errorf("%s: put has %d inline rc chains but %d poison checks under FERN_RC_FREE_DEBUG — a chain that reads a count without checking it is a use after free the sanitizer misses:\n%s",
				c.target, chains, got, pfn)
		}
		if c.poison.MatchString(fn) {
			t.Errorf("%s: put carries the poison check with the flag off:\n%s", c.target, fn)
		}
		// The out-of-line stub the inline form replaces carries it too: a
		// function the lift declines still calls it.
		stub := functionListing(string(pasm), "__fn___fern_rc_is_unique")
		if stub == "" {
			t.Fatalf("%s: no __fn___fern_rc_is_unique in the flag-on listing", c.target)
		}
		if !c.poison.MatchString(stub) {
			t.Errorf("%s: the uniqueness stub reads the count without the poison check:\n%s", c.target, stub)
		}
	}
}

// rcPoisonWord is the value a freed block's count is overwritten with, which
// the sanitizer's check compares against (asm_ir.san_poison_check). It is
// ast.RcPoison, and uafPoisonDec derives the same value from the same
// constant.
var rcPoisonWord = uafPoisonDec

// A constant the binary ops alone read is an immediate operand on both ISAs
// and is never materialised; one a division reads keeps its register on
// arm64 and is read by the literal-divisor forms on x86-64. The
// loop bound is under 4,096 so it is an immediate on arm64 too. A shift by a
// literal takes the immediate-count form, so neither the count register nor
// the mask a variable count needs appears.
func TestSelfHostSSAConstantsAreImmediates(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "count.fern")
	prog := `function count(): i64 {
    var sum: i64 = 0i64;
    var i: i64 = 0i64;
    while (i < 3000i64) { sum = sum + i; i = i + 1i64; }
    return (sum % 97i64) + (sum >> 3i64);
}
function main(): i32 { return count() as i32; }
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target       string
		want, absent []*regexp.Regexp
	}{
		// On x86-64 the constant divisor is not materialised either: the
		// literal-divisor form (ir_div_const) takes the i64 remainder by the
		// multiply-high reciprocal and multiplies the 97 back as an
		// immediate, so neither a divide nor the two guards a run-time
		// divisor needs appear.
		{"x86-64-linux",
			[]*regexp.Regexp{regexp.MustCompile(`addq \$1, %r`), regexp.MustCompile(`cmpq \$3000, %r`), regexp.MustCompile(`movabsq \$-6275696437447579415, %rax`), regexp.MustCompile(`imulq \$97, %r`), regexp.MustCompile(`sarq \$3, %r`)},
			[]*regexp.Regexp{regexp.MustCompile(`mov[ql] \$3000, %`), regexp.MustCompile(`mov[ql] \$1, %`), regexp.MustCompile(`mov[ql] \$97, %`), regexp.MustCompile(`idivq`), regexp.MustCompile(`testq %rcx`), regexp.MustCompile(`cmpq \$-1`), regexp.MustCompile(`sarq %cl`), regexp.MustCompile(`andl \$31, %ecx`)}},
		{"arm64-linux",
			[]*regexp.Regexp{regexp.MustCompile(`add x\d+, x\d+, #1\n`), regexp.MustCompile(`cmp x\d+, #3000\n`), regexp.MustCompile(`mov x\d+, #97\n`), regexp.MustCompile(`asr x\d+, x\d+, #3\n`)},
			[]*regexp.Regexp{regexp.MustCompile(`mov x\d+, #3000\n`), regexp.MustCompile(`mov x\d+, #1\n`), regexp.MustCompile(`and x\d+, x\d+, #31\n`)}},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "count-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-backend", "ssa", "-emit", "asm", "-o", out, src, h.stdlib)
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_count")
		if fn == "" {
			t.Fatalf("%s: no __fn_count in the listing:\n%s", c.target, asm)
		}
		for _, re := range c.want {
			if !re.MatchString(fn) {
				t.Errorf("%s: count has no %s:\n%s", c.target, re, fn)
			}
		}
		for _, re := range c.absent {
			if re.MatchString(fn) {
				t.Errorf("%s: count still materialises %s:\n%s", c.target, re, fn)
			}
		}
	}
}

// TestSelfHostSSADynDispatchArgumentInScratch pins the one branch of the
// register path's dyn dispatch the behavioural gate cannot see firing: an
// argument whose home is the chain's own scratch register. arg_from_call
// hands the dispatch a value the call before it defined, so the allocator
// gives it the return register, and the wrapper moves it out before the chain
// reads the receiver's shape through that register. The listing shows the
// move and the pushed copy on x86-64, the move and the reload on arm64. An
// allocator change that stopped producing the shape would leave the branch
// untested again, which this test refuses.
func TestSelfHostSSADynDispatchArgumentInScratch(t *testing.T) {
	h := selfHostCLIForHost(t)
	var src string
	for _, p := range ssaBackendPrograms {
		if p.name == "dyn_dispatch" {
			src = p.src
		}
	}
	if src == "" {
		t.Fatal("the dyn_dispatch program is gone from ssaBackendPrograms")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "dyn.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		t.Run(tg.target, func(t *testing.T) {
			asm := filepath.Join(dir, tg.target+".s")
			h.compileWith(t, tg, path, asm, "-backend", "ssa", "-emit", "asm")
			text, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			fn := functionListing(string(text), "__fn_arg_from_call")
			move, use := "    movq %rax, %r11\n", "    pushq %r11\n"
			if strings.HasPrefix(tg.target, "arm64") {
				move, use = "    mov x4, x0\n", "    mov x0, x4\n"
			}
			i := strings.Index(fn, move)
			if i < 0 {
				t.Fatalf("arg_from_call never moves the call result out of the chain's scratch register:\n%s", fn)
			}
			if !strings.Contains(fn[i:], use) || !strings.Contains(fn[i:], "dynend") {
				t.Fatalf("the moved argument is not what the dispatch chain reads:\n%s", fn)
			}
		})
	}
}

// functionListing is the text of one function in an emitted listing: from
// its label to the next function label.
func functionListing(text, label string) string {
	start := strings.Index(text, label+":\n")
	if start < 0 {
		return ""
	}
	rest := text[start+len(label)+2:]
	// The function's register entry, `<label>.r:`, is inside its listing.
	from := 0
	for {
		end := strings.Index(rest[from:], "\n__fn_")
		if end < 0 {
			return rest
		}
		if strings.HasPrefix(rest[from+end+1:], label+".r:\n") {
			from += end + 1
			continue
		}
		return rest[:from+end]
	}
}
