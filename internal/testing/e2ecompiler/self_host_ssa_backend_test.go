package e2ecompiler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

type ssaBackendProgram struct {
	name string
	src  string
}

var ssaBackendPrograms = []ssaBackendProgram{
	{name: "defer_type_param", src: `
trait Mk { function make(): Self; function get(self: Self): i32; }
struct W { v: i32 }
impl Mk for W { function make(): W { return W { v: 41 }; } function get(self: W): i32 { return self.v; } }
function f[T: Mk](y: T, x: i32): i32 {
    let acc: i32 = 0;
    defer { acc = acc + T.make().get(); }
    return acc + x;
}
function main(): i32 { return f[W](W { v: 0 }, 1); }
`},
	{name: "with_chain", src: `
function hist(xs: i32[]): i32[] {
    let counts: i32[] = [0, 0, 0, 0];
    let i: i32 = 0;
    while (i < xs.len()) {
        let d0: i32 = xs[i] & 3;
        let d1: i32 = (xs[i] >> 2) & 3;
        counts = counts.with(d0, counts[d0] + 1);
        counts = counts.with(d1, counts[d1] + 1);
        i = i + 1;
    }
    return counts;
}
function main(): i32 {
    let c: i32[] = hist([1, 5, 9, 15, 4, 0]);
    let d: i32[] = c.with(0, 100);
    let kept: i32[] = d;
    d = d.with(1, 200);
    let e: i32[] = d.with(2, 7);
    e = e.with(3, e[3] + 1);
    if (c[0] != 4 || c[1] != 5 || kept[0] != 100 || kept[1] != c[1] || d[1] != 200 || d[2] != c[2] || e[2] != 7) { return 1; }
    let far: i32 = e.len() + 1;
    e = e.with(far, 0);
    return e[0];
}
`},
	{name: "fact", src: `
function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); }
function main(): i32 { return fact(5) - 100; }
`},
	{name: "fib_swap", src: `
function fib(n: i32): i32 {
    let a: i32 = 0;
    let b: i32 = 1;
    let i: i32 = 0;
    while (i < n) { let t: i32 = a; a = b; b = t + b; i = i + 1; }
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
    let m: i64 = mix(123456789, 987654321);
    let s: i32 = shifts(1000, 3) + shifts(7, 40);
    let u: i32 = unsigned(4000000000, 5) * 10 + unsigned(5, 4000000000);
    let r: i32 = narrow(m) + s + u;
    return r & 127;
}
`},
	{name: "reuse_cross_type_shape", src: `
struct P { x: i32, y: i32 }
struct Q { a: i32, b: i32 }
function f(): i32 {
    let p: P = P { x: 1, y: 2 };
    let t: i32 = p.x + p.y;
    let q: Q = Q { a: 3, b: 4 };
    return t + q.a + q.b;
}
function main(): i32 { return f(); }
`},
	{name: "heap_bump_bytes", src: `
function report(): i32 {
    let before: i64 = __heap_bump_bytes();
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < 500) { xs = xs.append(i); i = i + 1; }
    let after: i64 = __heap_bump_bytes();
    if (before > after) { return 1; }
    if (after == 0i64) { return 2; }
    if (xs.len() != 500) { return 3; }
    return 0;
}
function main(): i32 { return report(); }
`},
	{name: "control_flow", src: `
function first_square_over(limit: i32): i32 {
    let i: i32 = 0;
    while (i < 1000) {
        if (i * i > limit) { return i; }
        i = i + 1;
    }
    return 0 - 1;
}
function nested(n: i32): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let j: i32 = 0;
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
    let d: i32 = (0 - 2147483647 - 1) / (0 - 1);
    let e: i32 = (0 - 7) % 3;
    let k: i32 = first_square_over(50) + nested(5) + signs(0 - 9) + signs(0) + signs(4) + (d % 7) + e;
    if (!(k > 1000)) { k = k + 100; }
    return k & 255;
}
`},
	{name: "labelled_continue_defer", src: `
function count_up(i: i32, n: i32): i32 {
    while (i < n) { i = i + 1; }
    return i;
}
function main(): i32 {
    let seen: i32 = 0;
    let i: i32 = 0;
    outer: while (i < 3) {
        i = i + 1;
        loop {
            let items: i32[] = [0];
            defer seen = seen * 3 + items[0];
            items = [i];
            continue outer;
        }
    }
    return seen + count_up(4, 9);
}
`},
	{name: "invariant_through_phi", src: `
@noinline
function step(x: i32, i: i32): i32 { return x + i; }
function forward(x: i32, rounds: i32): i32 {
    let i: i32 = 0;
    let acc: i32 = x;
    while (i < rounds) { acc = step(acc, i); i = i + 1; }
    return acc;
}
@noinline
function bit(w: i32, i: i32): boolean { return ((w >> (i & 31)) & 1) == 1; }
function count_bits(w: i32, n: i32): i32 {
    let c: i32 = 0;
    for i in 0..(n + 1) { if (bit(w, i)) { c = c + 1; } }
    return c;
}
function main(): i32 { return (forward(3, 10) + count_bits(1431655765, 31) * 10) % 256; }
`},
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
function letters(): i32 { let s: string = "fern"; return s.len() * 100 + (s[1] as i32); }
function main(): i32 {
    let p: P = mk(3, 4);
    let xs: i32[] = [5, 6, 7];
    let t: (i32, i32) = pair(9);
    let total: i32 = sum(p) + third(xs) + t.0 + t.1 + unwrap(Some(11)) + unwrap(None)
        + area(Dot(2)) + area(Line(3, 4)) + area(Empty) + letters();
    return total % 256;
}
`},
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
    let s: string = join("fe", "rn");
    let xs: i32[] = grow([1, 2], 3);
    let n: i32 = xs.len() * 10 + s.len() + xs[2] + mid(s, 1, 3) + mid("abcdef", 2, 5) + mid("ab", 1, 9);
    if (same(s, "fern")) { n = n + 100; }
    if (before("apple", s)) { n = n + 1; }
    return n % 256;
}
`},
	{name: "byte_sieve", src: `
function sieve(n: i32): boolean[] {
    let s: boolean[] = [];
    for i in 0..(n + 1) { s = s.append(true); }
    if (n >= 0) { s = s.with(0, false); }
    if (n >= 1) { s = s.with(1, false); }
    let i: i32 = 2;
    while (i * i <= n) {
        if (s[i]) {
            let j: i32 = i * i;
            while (j <= n) { s = s.with(j, false); j = j + i; }
        }
        i = i + 1;
    }
    return s;
}
function count(s: boolean[]): i32 {
    let n: i32 = 0;
    for b in s { if (b) { n = n + 1; } }
    return n;
}
function main(): i32 { return count(sieve(1000)); }
`},
	{name: "json_array", src: `
import "core/cmp";
import "std/json";
function main(): i32 {
    match (json.parse("[1, [2, 3], [], {\"k\": [4]}]")) {
        Ok(v) => { return json.json_encode(v).len(); },
        Err(e) => { return 1; },
    }
}
`},
	{name: "floats", src: `
import "std/f64";
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
    let f: i32 = x.floor() as i32;
    let c: i32 = x.ceil() as i32;
    let t: i32 = x.trunc() as i32;
    let r: i32 = x.round() as i32;
    return f + c * 10 + t * 100 + r * 1000;
}
function convs(n: i32, u: u32, w: i64, v: u64): i64 {
    let a: f64 = n as f64;
    let b: f64 = u as f64;
    let c: f64 = w as f64;
    let d: f64 = v as f64;
    let s: f64 = a + b + c + d;
    return (s as i64) + ((s / 3.0) as i32) as i64 + (((0.0 - s) as u32) as i64) + ((s * 1e30) as i32) as i64;
}
function bits(x: f64): i64 {
    let b: i64 = f64_bits(x);
    let y: f64 = f64_from_bits(b + 1);
    let h: i32 = f32_bits(y as f32);
    let zf: f32 = f32_from_bits(h);
    let z: f64 = zf as f64;
    return b + (z as i64) + (h as i64) % 7;
}
function neg(x: f64): f64 { return (0.0 - x).abs() - (0.0 - x); }
function widen(x: f64): f64 { return area(2) + hyp(3, 4) + x; }
function signs(): i32 {
    let m: f64 = -2.5;
    let z: f64 = -0.0;
    let r: i32 = 0;
    if (m < 0.0) { r = r + 1; }
    if (f64_bits(z) != 0) { r = r + 2; }
    if (z == 0.0) { r = r + 4; }
    return r + (m.abs() * 2.0) as i32;
}
function trans(x: f64): i32 { return ((x.sin() * 1000.0) as i32) + ((x.cos() * 1000.0) as i32) + ((x.exp() * 10.0) as i32) + ((x.log() * 1000.0) as i32) + (x.pow(2.5) as i32); }
function main(): i32 {
    let acc: i64 = (area(2.0) * 1000.0) as i64;
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
	{name: "float_registers", src: `
import "std/i64";
import "std/f64";
function show(x: f64): void { print(f64_bits(x).to_string() + "\n"); }
function idn(x: f64, n: i32): f64 { if (n == 0) { return x; } return idn(x, n - 1); }
function poke(n: i32): i32 { if (n > 100) { return n; } return poke(n + 50) - 49; }
function pressure(x: f64): f64 {
    let a: f64 = x * 1.5;
    let b: f64 = x + 2.25;
    let c: f64 = x - 3.125;
    let d: f64 = x * x;
    let e: f64 = a / 7.0;
    let f: f64 = b * c;
    let g: f64 = d - e;
    let h: f64 = f + g;
    let i: f64 = a * b;
    let j: f64 = c * d;
    let k: f64 = e - f;
    let l: f64 = g / 3.0;
    let n: i32 = poke(3);
    let m: f64 = idn(h + i, 3);
    let o: f64 = idn(m * 0.5, 2);
    return a + b + c + d + e + f + g + h + i + j + k + l + m + o + (n as f64);
}
function series(n: i32): f64 {
    let s: f64 = 0.0;
    let t: f64 = 1.0;
    let i: i32 = 0;
    while (i < n) {
        s = s + t / (i as f64 + 1.0);
        if (i % 3 == 0) { t = 0.0 - t; }
        i = i + 1;
    }
    return s;
}
function pick(c: boolean, a: f64, b: f64): f64 {
    let r: f64 = 0.5;
    if (c) { r = b * a; }
    return r - 1.0;
}
function mixed(x: f64): f64 {
    let b: i64 = f64_bits(x);
    let y: f64 = f64_from_bits(b + 1);
    return y * 2.0 + x;
}
function cmp(a: f64, b: f64): i32 {
    let r: i32 = 0;
    if (a < b) { r = r + 1; }
    if (a <= b) { r = r + 2; }
    if (a > b) { r = r + 4; }
    if (a >= b) { r = r + 8; }
    if (a == b) { r = r + 16; }
    if (a != b) { r = r + 32; }
    return r;
}
function many(a: f64, b: f64, c: f64, d: f64, e: f64, f: f64, g: f64, h: f64, i: f64, j: f64): f64 {
    return a - b + c * d - e / f + g * h - i + j;
}
function main(): i32 {
    show(pressure(1.75));
    show(series(1000));
    show(pick(true, 3.0, 2.0) + pick(false, 3.0, 2.0));
    show(mixed(3.0));
    show(many(1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0));
    show(0.0 - idn(2.5, 1));
    show((2.0).sqrt() + (2.5).floor() + (2.5).ceil() + (0.0 - 2.5).trunc() + (0.0 - 2.5).abs());
    let nan: f64 = 0.0 / 0.0;
    return cmp(1.0, 2.0) + cmp(2.0, 2.0) * 3 + cmp(nan, 2.0) * 5;
}
`},
	{name: "host_calls", src: `
import "std/i32";
function probe_env(): i32 {
    let n: i32 = 0;
    match (env("FERN_SSA_HOST_PROBE_UNSET")) { Some(v) => { n = n + 100; }, None => { n = n + 1; } }
    match (env("PATH")) { Some(v) => { if (v.len() > 0) { n = n + 2; } }, None => { n = n + 200; } }
    return n;
}
function probe_fs(path: string): i32 {
    let n: i32 = 0;
    match (read_file(path)) { Ok(s) => { n = n + 300; }, Err(e) => { n = n + 4; } }
    match (stat("/")) { Ok(st) => { n = n + 8; }, Err(e) => { n = n + 400; } }
    match (stat(path)) { Ok(st) => { n = n + 500; }, Err(e) => { n = n + 16; } }
    return n;
}
function probe_clock(): i32 {
    let a: i64 = monotonic_ns();
    let b: i64 = monotonic_ns();
    if (a > 0 && b >= a) { return 32; }
    return 600;
}
function main(): i32 {
    let n: i32 = probe_env() + probe_fs("/nonexistent/fern/ssa/host/probe") + probe_clock();
    print("host " + n.to_string() + "\n");
    exit(n % 100);
    return 7;
}
`},
	{name: "os_floor", src: `
import "std/i32";
function probe_ids(): i32 {
    let n: i32 = 0;
    if (geteuid() < 1000000000) { n = n + 1; }
    if (cpu_count() > 0) { n = n + 2; }
    if (hostname().len() > 0) { n = n + 4; }
    match (getcwd()) { Ok(wd) => { if (wd.len() > 0) { n = n + 8; } }, Err(e) => {} }
    if (uname_field(0).len() > 0) { n = n + 16; }
    let old: i32 = umask(18);
    if (umask(old) == 18) { n = n + 32; }
    return n;
}
function probe_handle(path: string): i32 {
    let n: i32 = 0;
    match (open_reader(path)) {
        Ok(r) => {
            match (r.stat()) { Ok(st) => { n = n + 100; }, Err(e) => { n = n + 1000; } }
            match (r.flags()) { Ok(f) => { n = n + 200; }, Err(e) => { n = n + 2000; } }
            if (!r.isatty()) { n = n + 400; }
            match (r.close()) { Err(e) => { n = n + 4000; }, Ok(_) => { n = n + 800; } }
        },
        Err(e) => { n = n + 8000; },
    }
    return n;
}
function main(): i32 {
    let n: i32 = probe_ids() + probe_handle("/dev/null") + probe_handle("/nonexistent/fern/ssa/os/floor");
    print("os " + n.to_string() + "\n");
    return n % 100;
}
`},
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
@noinline
function arg_from_call(s: dyn Shape, n: i32): i32 { return s.scaled(bump(n)); }
function main(): i32 {
    let a: dyn Shape = Square { side: 3 };
    let b: dyn Shape = Round(5);
    let c: dyn Shape = Flat;
    let d: dyn Shape = 7;
    let total: i32 = measure(a, 2) + measure(b, 3) + measure(c, 4) + measure(d, 5) + arg_from_call(a, 1);
    print("dyn " + total.to_string() + "\n");
    return total % 256;
}
`},
	{name: "maps", src: `
import "core/map";
import "std/i32";
function build(n: i32): Map[string, i32] {
    let m: Map[string, i32] = Map { };
    let i: i32 = 0;
    while (i < n) { m = m.insert("k" + i.to_string(), i * 3); i = i + 1; }
    return m;
}
function main(): i32 {
    let m: Map[string, i32] = build(50);
    let ints: Map[i32, i32] = Map { };
    let j: i32 = 0;
    while (j < 40) { ints = ints.insert(j * 7, j); j = j + 1; }
    let total: i32 = m.get_or("k7", 0) + m.get_or("zz", 100) + ints.get_or(21, 0) + ints.get_or(22, 1000);
    if (m.contains("k9")) { total = total + 1; }
    if (!ints.contains(5)) { total = total + 2; }
    total = total + m.len() * 10 + ints.keys().len() + ints.values().len();
    for k in m.keys() { if (k.len() == 2) { total = total + 1; } }
    return total % 200;
}
`},
	{name: "folded_aggregates", src: `
import "std/pmap" as pmap;
import "std/option";
import "std/i32";
import "core/cmp";

@derive(cmp.Eq)
struct Coarse { bucket: i32, id: i32 }
impl cmp.Hash for Coarse { function hash(self: Coarse): i32 { return self.bucket; } }
function main(): i32 {
    let m: pmap.PMap[Coarse, i32] = pmap.pmap_new();
    let i: i32 = 0;
    while (i < 40) { m = m.insert(Coarse { bucket: i % 5, id: i }, i); i = i + 1; }
    let n: i32 = m.get_or(Coarse { bucket: 3, id: 13 }, -1) * 10 + m.get_or(Coarse { bucket: 3, id: 14 }, -1);
    if (m.contains(Coarse { bucket: 1, id: 6 })) { n = n + 1000; }
    return n % 251;
}
`},
	{name: "strings", src: `import "std/unicode";

import "std/string";
import "std/i32";
import "std/i64";
function strs(s: string): i32 {
    let n: i32 = 0;
    match (s.index_of("fern")) { Some(i) => { n = n + i; }, None => { n = n - 1; } }
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
    let s: string = "  the fern language\n has a fern end";
    return (strs(s) + bits(15790080, 280375465082880)) % 251;
}
`},
	{name: "slice_bound_reuse", src: `
function tail(s: string, lo: i32, hi: i32): i32 {
    let v: str = slice_unchecked(s, lo, hi);
    let x: i32 = hi + 1;
    return x * 1000 + v.len();
}
function main(): i32 {
    let s: string = "abcdefghijkl";
    return (tail(s, 3, 9) + tail(s, 0, 4) + tail(s, 5, 12)) % 251;
}
`},
	{name: "loop_fallthrough", src: `
import "std/ordmap" as ordmap;
import "core/cmp";
function main(): i32 {
    let m: ordmap.OrdMap[i32, i32] = ordmap.ordmap_new();
    let i: i32 = 0;
    while (i < 30) { m = m.insert((i * 7) % 31, i); i = i + 1; }
    let total: i32 = m.fold(0, (acc: i32, k: i32, v: i32) => acc + k * 2 + v);
    return (total + m.len()) % 251;
}
`},
	{name: "mixed_module", src: `
import "std/i32";
function sum_to(n: i32): i32 { let s: i32 = 0; let i: i32 = 1; while (i <= n) { s = s + i; i = i + 1; } return s; }
function gcd(a: i32, b: i32): i32 { while (b != 0) { let t: i32 = b; b = a % b; a = t; } return a; }
function eight(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32): i32 { return a - b + c - d + e - f + g - h; }
function main(): i32 {
    print(sum_to(100).to_string());
    print(gcd(1071, 462).to_string());
    print(eight(1, 2, 3, 4, 5, 6, 7, 8).to_string());
    let xs: i32[] = [3, 1, 2];
    print((xs.len() + gcd(xs[0], xs[2])).to_string());
    return 3;
}
`},
	{name: "immediates", src: `
import "std/i32";
function mix(x: i32, y: i64, u: u32): i32 {
    let a: i32 = x + 7;
    let b: i32 = 7 - x;
    let c: i32 = x - 7;
    let d: i32 = 3 * x;
    let e: i32 = x & 255;
    let f: i32 = 4096 | x;
    let g: i32 = x ^ -1;
    let h: i32 = x * 1000003;
    let w: i32 = x + 2147483647;
    let k: i32 = 0;
    if (x < 10) { k = k + 1; }
    if (10 < x) { k = k + 2; }
    if (x == -1) { k = k + 4; }
    if (-1 != x) { k = k + 8; }
    if (y >= 3000000i64) { k = k + 16; }
    if (3000000i64 > y) { k = k + 32; }
    if (u < 4000000000u32) { k = k + 64; }
    if (u > 100u32) { k = k + 128; }
    if (5000i64 - y < 0i64) { k = k + 256; }
    let m: i64 = (y + 5000i64) * 3i64 - (y % 7i64) + (y >> 2i64);
    let q: i32 = a + b + c + d + e + f + g + h + w + k + (x % 7) + (x / 3) + (m as i32);
    let i: i32 = 0;
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
	{name: "carried_pairs", src: `
import "std/i32";
function old_after_latch(n: i32): i32 {
    let sum: i32 = 0;
    let i: i32 = 0;
    let old: i32 = 0;
    while (true) {
        old = sum;
        sum = sum + i * 3;
        i = i + 1;
        if (i > n) { break; }
    }
    return old * 1000 + sum;
}
function swap(n: i32): i32 {
    let a: i32 = 1;
    let b: i32 = 2;
    let i: i32 = 0;
    while (i < n) { let t: i32 = a; a = b; b = t; i = i + 1; }
    return a * 10 + b;
}
function inner_reads_outer(n: i32): i32 {
    let acc: i32 = 1;
    let o: i32 = 0;
    while (o < n) {
        let k: i32 = 0;
        let step: i32 = 0;
        while (k < 3) { step = step + acc + k; k = k + 1; }
        acc = step;
        o = o + 1;
    }
    return acc;
}
function two_latches(n: i32): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        i = i + 1;
        if (i % 3 == 0) { s = s + 100; continue; }
        s = s + i;
    }
    return s;
}
function exit_reads_both(n: i32): i32 {
    let s: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        let next: i32 = s + i;
        if (next > 50) { return next * 7 + s; }
        s = next;
        i = i + 1;
    }
    return s;
}
function shifted(n: i32): i32 {
    let x: i32 = 1;
    let i: i32 = 0;
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
	{name: "empty_blocks", src: `
function empties(n: i32): i32 {
    let k: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        if (i % 2 == 0) { } else { }
        if (i > 3) { let dead: i32 = i * 9; }
        while (k > 100) { k = k - 1000; }
        k = k + i;
        i = i + 1;
    }
    let j: i32 = 0;
    while (j < n) { let unused: i32 = j * 2; j = j + 1; }
    if (n > 2) { } else { k = k + 7; }
    return k + j;
}
function main(): i32 { return empties(6) * 3 + empties(1); }
`},
}

type ssaBackendTarget struct {
	target string
	runner []string
}

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

func selfHostCLIForHost(t *testing.T) ssaBackendHost {
	t.Helper()
	ssaHostOnce.Do(func() {
		cliTarget, targets, skip := hostTargets()
		if skip != "" {
			ssaHostSkip = skip
			return
		}
		fern := buildLangBinForInterp(t)
		src, err := filepath.Abs("../../../compiler/fern.fern")
		if err != nil {
			ssaHostSkip = err.Error()
			return
		}
		stdlib, err := filepath.Abs("../../stdlib")
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

func (h ssaBackendHost) compileWith(t *testing.T, tg ssaBackendTarget, src, out string, extra ...string) {
	t.Helper()
	args := append([]string{"-target", tg.target}, extra...)
	args = append(args, "-o", out, src, h.stdlib)
	if out, err := exec.Command(h.cli, args...).CombinedOutput(); err != nil {
		t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
	}
}

func (h ssaBackendHost) compileNative(t *testing.T, tg ssaBackendTarget, src, out string) {
	t.Helper()
	args := []string{"-target", tg.target, "-o", out, src}
	if out, err := exec.Command(h.native, args...).CombinedOutput(); err != nil {
		t.Fatalf("native fern %v: %v\n%s", args, err, out)
	}
}

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
		out, err := exec.Command(h.cli, "-target", tg.target, "-backend", "flat", "-o", filepath.Join(dir, base+"_flat"), src, h.stdlib).CombinedOutput()
		if err == nil {
			t.Fatalf("%s: -backend flat was accepted", tg.target)
		}
		if !strings.Contains(string(out), "-backend flat is not available for -target "+tg.target) {
			t.Errorf("%s: refusal does not name the target: %s", tg.target, out)
		}
	}
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

func TestSelfHostSSAFrameIsSizedBySpills(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("function wide(n: i32): i32 {\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    let v%d: i32 = n + %d;\n", i, i)
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "    if (n > %d) { v%d = v%d + 1; }\n", i, i, (i+1)%200)
	}
	b.WriteString("    let s: i32 = 0;\n")
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

func TestSelfHostCLIBuildsForEveryNativeTarget(t *testing.T) {
	fern := buildLangBinForInterp(t)
	src, err := filepath.Abs("../../../compiler/fern.fern")
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

func TestSelfHostSSALoopIsFourInstructions(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "count.fern")
	prog := `@noinline function count(n: i64): i64 {
    let sum: i64 = 0i64;
    let i: i64 = 0i64;
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

func TestSelfHostSSAFloatsStayInFloatRegisters(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "horner.fern")
	prog := `@noinline function horner(x: f64, n: i32): f64 {
    let p: f64 = 0.5;
    let i: i32 = 0;
    while (i < n) { p = p * x + 0.25; i = i + 1; }
    return p;
}
function main(): i32 { return (horner(0.5, 40) * 100.0) as i32; }
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		arith  *regexp.Regexp
		out    *regexp.Regexp
	}{
		{"x86-64-linux", regexp.MustCompile(`(?m)^\s+(mulsd|addsd) %xmm\d+, %xmm([3-9]|1[0-5])$`), regexp.MustCompile(`(?m)^\s+movq %xmm\d+, %r\w+$`)},
		{"arm64-linux", regexp.MustCompile(`(?m)^\s+(fmul|fadd) d(1[6-9]|2\d|3[01]|[89]|1[0-5]), d\d+, d\d+$`), regexp.MustCompile(`(?m)^\s+fmov x\d+, d\d+$`)},
	}
	for _, c := range cases {
		out := filepath.Join(dir, "horner-"+c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-emit", "asm", "-o", out, src, h.stdlib)
		report, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, report)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_horner")
		if fn == "" {
			t.Fatalf("%s: no __fn_horner in the listing:\n%s", c.target, asm)
		}
		if n := len(c.arith.FindAllString(fn, -1)); n != 2 {
			t.Errorf("%s: horner has %d multiplies and adds into a float register, want 2:\n%s", c.target, n, fn)
		}
		if n := len(c.out.FindAllString(fn, -1)); n > 1 {
			t.Errorf("%s: horner moves an f64 between register classes %d times:\n%s", c.target, n, fn)
		}
	}
}

func TestSelfHostSSAResultTakesDyingOperandRegister(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "step.fern")
	prog := `@noinline function step(x: i64, y: i64): i64 {
    let a: i64 = x * 3i64;
    let b: i64 = a + y;
    if (b > 100i64) { return b - 7i64; }
    return b;
}
@noinline function first_over(n: i64): i64 {
    let i: i64 = 0i64;
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
		product *regexp.Regexp
		add     string
		jump    *regexp.Regexp
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

func TestSelfHostSSARcPrimitivesAreInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "blk.fern")
	prog := `struct Blk { buf: i32[], note: string, n: i32 }
@noinline
function (b: Blk) put(i: i32, v: i32): Blk { return Blk { ...b, buf: b.buf.with(i, v) }; }
function main(): i32 {
    let b: Blk = Blk { buf: [0, 0, 0, 0], note: "a refcounted field the update carries", n: 0 };
    let i: i32 = 0;
    while (i < 4) { b = b.put(i, i + 1); i = i + 1; }
    return b.buf[3];
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		floor  *regexp.Regexp
		count  *regexp.Regexp
		absent []string
		poison *regexp.Regexp
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
		stub := functionListing(string(pasm), "__fn___fern_rc_is_unique")
		if stub == "" {
			t.Fatalf("%s: no __fn___fern_rc_is_unique in the flag-on listing", c.target)
		}
		if !c.poison.MatchString(stub) {
			t.Errorf("%s: the uniqueness stub reads the count without the poison check:\n%s", c.target, stub)
		}
	}
}

func TestSelfHostSSAStrEqComparesLengthsInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "streq.fern")
	prog := `function same(a: string, b: string): i32 {
    if (a == b) { return 1; }
    return 0;
}
function main(): i32 {
    let r: i32 = same("abc", "ab");
    r = r + same("abc", "abd") * 2;
    r = r + same("abcdefghij", "abcdefghij") * 4;
    r = r + same("", "") * 8;
    r = r + same("", "a") * 16;
    r = r + same("xyz", "xyz") * 32;
    return r;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]struct {
		lengths    *regexp.Regexp
		entry, abi string
		frame      string
		route      string
	}{
		"x86-64-linux": {regexp.MustCompile(`(?m)^\s+cmpq 8\(%r\w+\), %rdx$`),
			"call __fn___fern_str_eq.r\n", "call __fn___fern_str_eq\n", "pushq %rbp", "movq %rdx, %rax\n    movq %rcx, %rsi\n"},
		"arm64-linux": {regexp.MustCompile(`(?m)^\s+ldr x7, \[x\d+, #8\]\n\s+cmp x6, x7$`),
			"bl __fn___fern_str_eq.r\n", "bl __fn___fern_str_eq\n", "stp x29, x30", ""},
	}
	for _, tg := range h.targets {
		shape, ok := shapes[tg.target]
		if !ok {
			continue
		}
		asmPath := filepath.Join(dir, "streq-"+tg.target+".s")
		h.compileWith(t, tg, src, asmPath, "-backend", "ssa", "-emit", "asm")
		asm, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_main")
		if fn == "" {
			t.Fatalf("%s: no __fn_main in the listing:\n%s", tg.target, asm)
		}
		if n := len(shape.lengths.FindAllString(fn, -1)); n != 6 {
			t.Errorf("%s: main compares %d pairs of length words inline, want 6:\n%s", tg.target, n, fn)
		}
		if n := strings.Count(fn, shape.entry); n != 6 {
			t.Errorf("%s: main calls the helper's register entry %d times, want 6:\n%s", tg.target, n, fn)
		}
		if strings.Contains(fn, shape.abi) {
			t.Errorf("%s: main still makes the stack-ABI call:\n%s", tg.target, fn)
		}
		if shape.route != "" && strings.Contains(fn, shape.route) {
			t.Errorf("%s: main passes an operand through a scratch on the way to the helper:\n%s", tg.target, fn)
		}
		helper := functionListing(string(asm), "__fn___fern_str_eq")
		if end := strings.Index(helper, ".size __fn___fern_str_eq"); end >= 0 {
			helper = helper[:end]
		}
		if helper == "" {
			t.Fatalf("%s: no __fn___fern_str_eq in the listing", tg.target)
		}
		if strings.Contains(helper, shape.frame) {
			t.Errorf("%s: the helper builds a frame:\n%s", tg.target, helper)
		}
		bin := filepath.Join(dir, "streq-"+tg.target)
		h.compileWith(t, tg, src, bin, "-backend", "ssa")
		if _, code := h.runProduced(t, tg, bin); code != 44 {
			t.Errorf("%s: exit = %d, want 44 (4 + 8 + 32: only the three equal pairs compare equal)", tg.target, code)
		}
	}
}

func TestSelfHostSSAWithChainSettlesOnce(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "with_chain.fern")
	prog := `@noinline function hist(xs: i32[]): i32[] {
    let counts: i32[] = [0, 0, 0, 0, 0, 0, 0, 0];
    let i: i32 = 0;
    while (i < xs.len()) {
        let d0: i32 = xs[i] & 7;
        let d1: i32 = (xs[i] >> 3) & 7;
        counts = counts.with(d0, counts[d0] + 1);
        counts = counts.with(d1, counts[d1] + 1);
        i = i + 1;
    }
    return counts;
}
function main(): i32 {
    let c: i32[] = hist([1, 9, 17, 63, 8, 0]);
    return c[0] * 10 + c[1];
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]struct{ unique, oob string }{
		"x86-64-linux": {"cmpl $1, -8(%r", "jae __fern_oob_abort"},
		"arm64-linux":  {"cmp w5, #1", "b __fern_oob_abort"},
	}
	for _, tg := range h.targets {
		shape, ok := shapes[tg.target]
		if !ok {
			continue
		}
		asmPath := filepath.Join(dir, "with_chain-"+tg.target+".s")
		h.compileWith(t, tg, src, asmPath, "-backend", "ssa", "-emit", "asm")
		asm, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_hist")
		if fn == "" {
			t.Fatalf("%s: no __fn_hist in the listing:\n%s", tg.target, asm)
		}
		if n := strings.Count(fn, shape.unique); n != 2 {
			t.Errorf("%s: hist reads an rc against 1 %d times, want 2 (one uniqueness test and its copy arm's release):\n%s", tg.target, n, fn)
		}
		if n := strings.Count(fn, shape.oob); n != 2 {
			t.Errorf("%s: hist has %d bounds aborts, want 2 (the two element reads):\n%s", tg.target, n, fn)
		}
		bin := filepath.Join(dir, "with_chain-"+tg.target)
		h.compileWith(t, tg, src, bin, "-backend", "ssa")
		if _, code := h.runProduced(t, tg, bin); code != 45 {
			t.Errorf("%s: exit = %d, want 45 (c[0] = 4, c[1] = 5)", tg.target, code)
		}
	}
}

func TestSelfHostSSAStrEqTestsLiteralFirstByteInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "streqlit.fern")
	prog := `function code(s: string, t: string): i32 {
    if (s == "add") { return 1; }
    if (s == "and") { return 2; }
    if (s == "sub") { return 3; }
    if ("xor" == s) { return 4; }
    if (s == "") { return 5; }
    if (s == t) { return 6; }
    return 0;
}
function main(): i32 {
    let r: i32 = code("add", "q") + code("and", "q") * 2 + code("sub", "q") * 4 + code("xor", "q") * 8 + code("", "q") * 16;
    r = r + code("sum", "sum") + code("adz", "q") * 64 + code("sum", "q") * 64;
    return r;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	shapes := map[string]struct {
		sites func(fn string) []byteSite
		entry string
	}{
		"x86-64-linux": {x86ByteSites, "call __fn___fern_str_eq.r\n"},
		"arm64-linux":  {arm64ByteSites, "bl __fn___fern_str_eq.r\n"},
	}
	const wantSites = "115:first 120:second 97:first 97:first"
	for _, tg := range h.targets {
		shape, ok := shapes[tg.target]
		if !ok {
			continue
		}
		asmPath := filepath.Join(dir, "streqlit-"+tg.target+".s")
		h.compileWith(t, tg, src, asmPath, "-backend", "ssa", "-emit", "asm")
		asm, err := os.ReadFile(asmPath)
		if err != nil {
			t.Fatal(err)
		}
		fn := functionListing(string(asm), "__fn_code")
		if fn == "" {
			t.Fatalf("%s: no __fn_code in the listing:\n%s", tg.target, asm)
		}
		var got []string
		for _, site := range shape.sites(fn) {
			got = append(got, site.String())
		}
		sort.Strings(got)
		if g := strings.Join(got, " "); g != wantSites {
			t.Errorf("%s: code's byte compares are %q, want %q (the byte read must be the non-literal operand's):\n%s", tg.target, g, wantSites, fn)
		}
		if n := strings.Count(fn, shape.entry); n != 6 {
			t.Errorf("%s: code calls the helper's register entry %d times, want 6:\n%s", tg.target, n, fn)
		}
		bin := filepath.Join(dir, "streqlit-"+tg.target)
		h.compileWith(t, tg, src, bin, "-backend", "ssa")
		if _, code := h.runProduced(t, tg, bin); code != 1+2*2+3*4+4*8+5*16+6 {
			t.Errorf("%s: exit = %d, want %d: each literal matched, the equal non-literals matched, the near misses rejected", tg.target, code, 1+2*2+3*4+4*8+5*16+6)
		}
	}
}

type byteSite struct {
	lit     string
	operand string
}

func (b byteSite) String() string { return b.lit + ":" + b.operand }

func whichOperand(loaded, first, second string) string {
	switch loaded {
	case first:
		return "first"
	case second:
		return "second"
	}
	return "neither(" + loaded + ")"
}

func x86ByteSites(fn string) []byteSite {
	lines := listingLines(fn)
	var (
		lenA = regexp.MustCompile(`^movq 8\((\S+)\), %rdx$`)
		lenB = regexp.MustCompile(`^cmpq 8\((\S+)\), %rdx$`)
		load = regexp.MustCompile(`^movq \((\S+)\), %rdx$`)
		cmpb = regexp.MustCompile(`^cmpb \$(\d+), \(%rdx\)$`)
	)
	var out []byteSite
	for i, l := range lines {
		m := cmpb.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		if i < 4 {
			out = append(out, byteSite{m[1], "no-preamble"})
			continue
		}
		a, b, r := lenA.FindStringSubmatch(lines[i-4]), lenB.FindStringSubmatch(lines[i-3]), load.FindStringSubmatch(lines[i-1])
		if a == nil || b == nil || r == nil {
			out = append(out, byteSite{m[1], "no-preamble"})
			continue
		}
		out = append(out, byteSite{m[1], whichOperand(r[1], a[1], b[1])})
	}
	return out
}

func arm64ByteSites(fn string) []byteSite {
	lines := listingLines(fn)
	var (
		lenA = regexp.MustCompile(`^ldr x6, \[(\S+), #8\]$`)
		lenB = regexp.MustCompile(`^ldr x7, \[(\S+), #8\]$`)
		load = regexp.MustCompile(`^ldr x6, \[(\S+)\]$`)
		cmp  = regexp.MustCompile(`^cmp w6, #(\d+)$`)
	)
	var out []byteSite
	for i, l := range lines {
		m := cmp.FindStringSubmatch(l)
		if m == nil || i < 1 || lines[i-1] != "ldrb w6, [x6]" {
			continue
		}
		if i < 6 {
			out = append(out, byteSite{m[1], "no-preamble"})
			continue
		}
		a, b, r := lenA.FindStringSubmatch(lines[i-6]), lenB.FindStringSubmatch(lines[i-5]), load.FindStringSubmatch(lines[i-2])
		if a == nil || b == nil || r == nil {
			out = append(out, byteSite{m[1], "no-preamble"})
			continue
		}
		out = append(out, byteSite{m[1], whichOperand(r[1], a[1], b[1])})
	}
	return out
}

func listingLines(fn string) []string {
	var out []string
	for _, l := range strings.Split(fn, "\n") {
		out = append(out, strings.TrimSpace(l))
	}
	return out
}

var rcPoisonWord = uafPoisonDec

func TestSelfHostSSAConstantsAreImmediates(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "count.fern")
	prog := `@noinline function count(): i64 {
    let sum: i64 = 0i64;
    let i: i64 = 0i64;
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

func TestSelfHostSSASpilledResultIsReadFromScratch(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	type quad struct{ a, b, c, d int }
	quads := []quad{{0, 4, 8, 12}, {1, 5, 9, 13}, {2, 6, 10, 14}, {3, 7, 11, 15}, {0, 5, 10, 15}, {1, 6, 11, 12}, {2, 7, 8, 13}, {3, 4, 9, 14}}
	var b strings.Builder
	b.WriteString("@noinline function mix(seed: u64, rounds: i32): u64 {\n")
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "    let v%d: u64 = seed * %d as u64;\n", i, 2*i+3)
		fmt.Fprintf(&b, "    let m%d: u64 = seed ^ %d as u64;\n", i, 7*i+1)
	}
	b.WriteString("    let r: i32 = 0;\n    while (r < rounds) {\n")
	rot := func(dst, x, y string, n int) {
		fmt.Fprintf(&b, "        %s = (%s ^ %s) >> %d as u64 | (%s ^ %s) << %d as u64;\n", dst, x, y, n, x, y, 64-n)
	}
	for g, q := range quads {
		va, vb, vc, vd := fmt.Sprintf("v%d", q.a), fmt.Sprintf("v%d", q.b), fmt.Sprintf("v%d", q.c), fmt.Sprintf("v%d", q.d)
		fmt.Fprintf(&b, "        %s = %s + %s + m%d;\n", va, va, vb, 2*g)
		rot(vd, vd, va, 32)
		fmt.Fprintf(&b, "        %s = %s + %s;\n", vc, vc, vd)
		rot(vb, vb, vc, 24)
		fmt.Fprintf(&b, "        %s = %s + %s + m%d;\n", va, va, vb, 2*g+1)
		rot(vd, vd, va, 16)
		fmt.Fprintf(&b, "        %s = %s + %s;\n", vc, vc, vd)
		rot(vb, vb, vc, 63)
	}
	b.WriteString("        r = r + 1;\n    }\n    return v0")
	for i := 1; i < 16; i++ {
		fmt.Fprintf(&b, " ^ v%d", i)
	}
	b.WriteString(";\n}\nfunction main(): i32 { return (mix(0x9e3779b97f4a7c15 as u64, 3) % (251 as u64)) as i32; }\n")
	src := filepath.Join(dir, "mix.fern")
	if err := os.WriteFile(src, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	seed := uint64(0x9e3779b97f4a7c15)
	var v, m [16]uint64
	for i := range v {
		v[i], m[i] = seed*uint64(2*i+3), seed^uint64(7*i+1)
	}
	for r := 0; r < 3; r++ {
		for g, q := range quads {
			v[q.a] += v[q.b] + m[2*g]
			v[q.d] = bits.RotateLeft64(v[q.d]^v[q.a], -32)
			v[q.c] += v[q.d]
			v[q.b] = bits.RotateLeft64(v[q.b]^v[q.c], -24)
			v[q.a] += v[q.b] + m[2*g+1]
			v[q.d] = bits.RotateLeft64(v[q.d]^v[q.a], -16)
			v[q.c] += v[q.d]
			v[q.b] = bits.RotateLeft64(v[q.b]^v[q.c], -63)
		}
	}
	var want uint64
	for _, x := range v {
		want ^= x
	}

	shapes := map[string]struct {
		store  *regexp.Regexp
		reload string
	}{
		"x86-64-linux": {regexp.MustCompile(`^movq %r11, (-?\d+\(%rbp\))$`), `^\S+ %s, %%\w+$`},
		"arm64-linux":  {regexp.MustCompile(`^str x\d+, (\[sp, #\d+\])$`), `^ldr x\d+, %s$`},
		"arm64-darwin": {regexp.MustCompile(`^str x\d+, (\[sp, #\d+\])$`), `^ldr x\d+, %s$`},
	}
	for _, tg := range h.targets {
		shape := shapes[tg.target]
		out := filepath.Join(dir, "mix-"+tg.target+".s")
		h.compileWith(t, tg, src, out, "-emit", "asm")
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		fn := listingLines(functionListing(string(asm), "__fn_mix"))
		spills := 0
		for i := 0; i+1 < len(fn); i++ {
			st := shape.store.FindStringSubmatch(fn[i])
			if st == nil {
				continue
			}
			spills++
			if regexp.MustCompile(fmt.Sprintf(shape.reload, regexp.QuoteMeta(st[1]))).MatchString(fn[i+1]) {
				t.Errorf("%s: mix reads %s back right after storing it:\n%s\n%s", tg.target, st[1], fn[i], fn[i+1])
			}
		}
		if spills == 0 {
			t.Fatalf("%s: mix spills nothing, so the shape is not exercised:\n%s", tg.target, strings.Join(fn, "\n"))
		}
		bin := filepath.Join(dir, "mix-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != int(want%251) {
			t.Errorf("%s: mix exits %d, want %d", tg.target, code, want%251)
		}
	}
}

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

func functionListing(text, label string) string {
	start := strings.Index(text, label+":\n")
	if start < 0 {
		return ""
	}
	rest := text[start+len(label)+2:]
	from := 0
	for {
		end := strings.Index(rest[from:], "\n__fn_")
		if end < 0 {
			return rest
		}
		if next := rest[from+end+1:]; strings.HasPrefix(next, label+".r:\n") || strings.HasPrefix(next, label+".k:\n") {
			from += end + 1
			continue
		}
		return rest[:from+end]
	}
}
