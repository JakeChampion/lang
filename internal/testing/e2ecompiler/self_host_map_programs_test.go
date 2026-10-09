package e2ecompiler

import "testing"

// mapProgramCases are map programs whose exit code is the answer: wide and
// float values, keyed and string columns, iteration, snapshots, deletes, a
// map in a struct field and in a tuple element, and the build-and-drop loops
// whose heap readout and underflow count catch a leaked or over-released
// entry (1 = the heap grew, 99 = an over-release, 88 = a wrong value read
// back). Each runs on every target through the CLI, on core/map (#9608).
var mapProgramCases = []struct {
	name string
	src  string
	want int
}{
	{"f64-value/get_or-hit", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 2.5 }; return (m.get_or(1, 0.0) * 2.0) as i32; }`, 5},
	{"f64-value/get_or-miss", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 2.5 }; return (m.get_or(9, 3.0)) as i32; }`, 3},
	{"f64-value/values-sum", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 1.5, 2: 2.5 }; let vs: f64[] = m.values(); let s: f64 = 0.0; let i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return s as i32; }`, 4},
	{"f64-value/get-some", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 2.5 }; match (m.get(1)) { Some(v) => { return (v * 2.0) as i32; }, None => { return 0; } } }`, 5},
	{"f64-value/foreach-sum", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = Map { 1: 1.5, 2: 2.5, 3: 4.0 }; let s: f64 = 0.0; for (k, v) in m { s = s + v; } return s as i32; }`, 8},
	{"f64-value/overwrite-reclaim", `import "core/map";
function main(): i32 { let m: Map[i32, f64] = map_new(8); m = m.insert(1, 2.5); m = m.insert(1, 7.0); if (__rc_underflow_count() != 0) { return 99; } return (m.get_or(1, 0.0) * 2.0) as i32; }`, 14},
	{"get-w64/hit", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007 }; match (m.get(1)) { Some(v) => { return (v % 1000) as i32; }, None => { return 42; } } }`, 7},
	{"get-w64/miss", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007 }; match (m.get(9)) { Some(v) => { return (v % 1000) as i32; }, None => { return 42; } } }`, 42},
	{"get-w64/u64-hit-shift", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; match (m.get(1)) { Some(v) => { return (v >> 58) as i32; }, None => { return 0; } } }`, 62},
	{"get-w64/insert-hit", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = map_new(8); let x: i64 = 9000000000; m = m.insert(3, x); match (m.get(3)) { Some(v) => { return (v % 1000) as i32; }, None => { return 7; } } }`, 0},
	{"i64-value/i64-literal", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000007 }; let g: i64 = m.get_or(1, 0); return (g % 1000) as i32; }`, 7},
	{"i64-value/u64-cast-shift", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; return (m.get_or(1, 0 as u64) >> 58) as i32; }`, 62},
	{"i64-value/i64-var-insert", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = map_new(8); let v: i64 = 9000000000; m = m.insert(1, v); return (m.get_or(1, 0) % 1000) as i32; }`, 0},
	{"i64-value/u64-insert-shift", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = map_new(8); m = m.insert(1, 18000000000000000000 as u64); return (m.get_or(1, 0 as u64) >> 58) as i32; }`, 62},
	{"i64-value/i64-unannotated-getor", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 2: 12000000005 }; let g = m.get_or(2, 0); return (g % 1000) as i32; }`, 5},
	{"i64-value/i64-default-miss", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = map_new(8); let g: i64 = m.get_or(99, 7000000009); return (g % 1000) as i32; }`, 9},
	{"i64-value/i64-overwrite-churn", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = map_new(8); let i: i32 = 0; while (i < 200) { m = m.insert(1, (i as i64) * 3000000000); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return (m.get_or(1, 0) % 1000) as i32; }`, 0},
	{"i64-value/i64-build-drop-reclaim", `import "core/map";
function build(): i32 { let m: Map[i32, i64] = map_new(8); let i: i32 = 0; while (i < 8) { m = m.insert(i, (i as i64) * 5000000000); i = i + 1; } if (m.get_or(2, 0) != 10000000000) { return 1; } return 0; } function main(): i32 { let bad: i32 = 0; let k: i32 = 0; while (k < 300) { if (build() != 0) { bad = 1; } k = k + 1; } if (__rc_underflow_count() != 0) { return 99; } if (bad != 0) { return 88; } return 0; }`, 0},
	{"iter/keys-string", `import "core/map";
function f(): i32 { let m: Map[string, i32] = map_new(0); m = m.insert("a", 7); m = m.insert("b", 8); let s: i32 = 0; for k in m.keys() { s = s + m.get_or(k, 0); } return s; }
function main(): i32 { return f(); }`, 15},
	{"iter/values-i32", `import "core/map";
function f(): i32 { let m: Map[string, i32] = map_new(0); m = m.insert("a", 7); m = m.insert("b", 8); let s: i32 = 0; for v in m.values() { s = s + v; } return s; }
function main(): i32 { return f(); }`, 15},
	{"iter/keys-i32", `import "core/map";
function f(): i32 { let m: Map[i32, i32] = __map_new_i32(0); m = m.insert(3, 10); m = m.insert(4, 20); let s: i32 = 0; for k in m.keys() { s = s + k; } return s; }
function main(): i32 { return f(); }`, 7},
	{"iter/keys-continue", `import "core/map";
function f(): i32 { let m: Map[i32, i32] = __map_new_i32(0); m = m.insert(1, 0); m = m.insert(2, 0); m = m.insert(3, 0); let s: i32 = 0; for k in m.keys() { if (k % 2 == 0) { continue; } s = s + k; } return s; }
function main(): i32 { return f(); }`, 4},
	{"iter-w64/foreach-sum", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000000, 2: 9000000000, 3: 12000000005 }; let s: i64 = 0; for (k, v) in m { s = s + v; } return (s % 1000) as i32; }`, 5},
	{"iter-w64/u64-foreach-shift", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64, 2: 1 as u64 }; let acc: u64 = 0; for (k, v) in m { acc = acc | v; } return (acc >> 63) as i32; }`, 1},
	{"iter-w64/insert-overwrite-iter", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = map_new(8); m = m.insert(1, 5000000000); m = m.insert(2, 3000000000); m = m.insert(1, 7000000000); let s: i64 = 0; for (k, v) in m { s = s + v; } if (__rc_underflow_count() != 0) { return 99; } return (s % 1000) as i32; }`, 0},
	{"ks-reclaim/mapks-key-column-flat-arm64", `import "core/map";
function build_sk(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "b": 1, "c" + "d": 2 };
    return 1;
}
function build_ik(n: i32): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_sk(i) + build_ik(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_sk(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ik(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapkvs-both-columns-flat-arm64", `import "core/map";
function build_ss(n: i32): i32 {
    let m: Map[string, string] = Map { "a" + "b": "x" + "y", "c" + "d": "z" + "w" };
    return 1;
}
function build_ii(n: i32): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_ss(i) + build_ii(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_ss(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ii(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-key-correct-arm64", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let m: Map[string, i32] = Map { "hel" + "lo": 5, "wor" + "ld": 6 };
        if (m.get_or("hello", 0) != 5) { bad = 1; }
        if (m.get_or("world", 0) != 6) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-fresh-key-flat-arm64", `import "core/map";
function build_sk_over(n: i32): i32 {
    let m: Map[string, i32] = Map { "wo" + "rd": 0 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert("wo" + "rd", j); j = j + 1; }
    return 1;
}
function build_ik_over(n: i32): i32 {
    let m: Map[string, i32] = Map { "k": 0 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert("k", j); j = j + 1; }
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_sk_over(i) + build_ik_over(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_sk_over(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ik_over(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-fresh-key-correct-arm64", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let m: Map[string, i32] = Map { "wo" + "rd": 0 };
        let j: i32 = 0;
        while (j < 8) { m = m.insert("wo" + "rd", j); j = j + 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        if (m.len() != 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-aliased-key-arm64", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let key: string = "wo" + "rd";
        let m: Map[string, i32] = Map { "wo" + "rd": 0 };
        let j: i32 = 0;
        while (j < 8) { m = m.insert(key, j); j = j + 1; }
        if (key.len() != 4) { bad = 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-key-column-flat", `import "core/map";
function build_sk(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "b": 1, "c" + "d": 2 };
    return 1;
}
function build_ik(n: i32): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build_sk(i) + build_ik(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build_sk(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 2000) { acc = acc + build_ik(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapkvs-both-columns-flat", `import "core/map";
function build_ss(n: i32): i32 {
    let m: Map[string, string] = Map { "a" + "b": "x" + "y", "c" + "d": "z" + "w" };
    return 1;
}
function build_ii(n: i32): i32 {
    let m: Map[string, i32] = Map { "a": 1, "b": 2 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build_ss(i) + build_ii(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build_ss(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 2000) { acc = acc + build_ii(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-key-correct", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let m: Map[string, i32] = Map { "hel" + "lo": 5, "wor" + "ld": 6 };
        if (m.get_or("hello", 0) != 5) { bad = 1; }
        if (m.get_or("world", 0) != 6) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-aliased-key-excluded", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let s: string = "aa" + "bb";
        let m: Map[string, i32] = Map { s: 7 };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or("aabb", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-fresh-key-flat", `import "core/map";
function build_sk_over(n: i32): i32 {
    let m: Map[string, i32] = Map { "wo" + "rd": 0 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert("wo" + "rd", j); j = j + 1; }
    return 1;
}
function build_ik_over(n: i32): i32 {
    let m: Map[string, i32] = Map { "k": 0 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert("k", j); j = j + 1; }
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build_sk_over(i) + build_ik_over(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build_sk_over(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 2000) { acc = acc + build_ik_over(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-fresh-key-correct", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let m: Map[string, i32] = Map { "wo" + "rd": 0 };
        let j: i32 = 0;
        while (j < 8) { m = m.insert("wo" + "rd", j); j = j + 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        if (m.len() != 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-aliased-key", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let key: string = "wo" + "rd";
        let m: Map[string, i32] = Map { "wo" + "rd": 0 };
        let j: i32 = 0;
        while (j < 8) { m = m.insert(key, j); j = j + 1; }
        if (key.len() != 4) { bad = 1; }
        if (m.get_or("word", 0) != 7) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-key-column-flat-wasm", `import "core/map";
function build_sk(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "b": 1, "c" + "d": 2 };
    return 1;
}
function build_ik(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_sk(i) + build_ik(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_sk(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ik(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapkvs-both-columns-flat-wasm", `import "core/map";
function build_ss(n: i32): i32 {
    let m: Map[string, string] = Map { "a" + "b": "x" + "y", "c" + "d": "z" + "w" };
    return 1;
}
function build_ii(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_ss(i) + build_ii(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_ss(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ii(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-key-correct-wasm", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let m: Map[string, string] = Map { "hel" + "lo": "aa" + "bb", "wor" + "ld": "cc" + "dd" };
        if (m.get_or("hello", "").len() != 4) { bad = 1; }
        if (m.get_or("world", "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"ks-reclaim/mapks-overwrite-fresh-key-flat-wasm", `import "core/map";
function build_sk_over(n: i32): i32 {
    let m: Map[string, i32] = Map { "wo" + "rd": 0 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert("wo" + "rd", j); j = j + 1; }
    return 1;
}
function build_ik_over(n: i32): i32 {
    let m: Map[i32, i32] = Map { 7: 0 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert(7, j); j = j + 1; }
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_sk_over(i) + build_ik_over(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_sk_over(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ik_over(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-keys-snapshot-semantics-arm64", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10, 2: 20 };
    let ks = m.keys();
    let vs: i32[] = m.values();
    m = m.insert(9, 90);
    m = m.insert(10, 100);
    m = m.insert(11, 110);
    m = m.insert(1, 11);
    if (ks.len() != 2) { return 10; }
    if (vs.len() != 2) { return 11; }
    let sv: i32 = 0;
    let i: i32 = 0;
    while (i < vs.len()) { sv = sv + vs[i]; i = i + 1; }
    if (sv != 30) { return 12; }
    if (m.len() != 5) { return 13; }
    if (m.get_or(1, 0) != 11) { return 14; }
    if (m.get_or(10, 0) != 100) { return 15; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"snapshot/map-i32-grow-churn-flat-arm64", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2 };
    let j: i32 = 0;
    while (j < 12) { m = m.insert(j + 10, j * 2); j = j + 1; }
    if (m.has(15)) { return m.len(); }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-keys-taken-churn-flat-arm64", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert(j + 10, j); j = j + 1; }
    let ks = m.keys();
    let vs = m.values();
    return ks.len() + vs.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-kv-iter-mutate-snapshot-arm64", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
    let total: i32 = 0;
    for (k, v) in m {
        total = total + k + v;
        m = m.insert(k + 100, v);
    }
    if (total != 66) { return 50; }
    if (m.len() != 6) { return 51; }
    if (m.get_or(102, 0) != 20) { return 52; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"snapshot/map-i32-grow-churn-flat", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2 };
    let j: i32 = 0;
    while (j < 12) { m = m.insert(j + 10, j * 2); j = j + 1; }
    if (m.has(15)) { return m.len(); }
    return 0;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-keys-taken-churn-flat", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2 };
    let j: i32 = 0;
    while (j < 8) { m = m.insert(j + 10, j); j = j + 1; }
    let ks = m.keys();
    let vs = m.values();
    return ks.len() + vs.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-kv-iter-churn-flat", `import "core/map";
function build(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4, 5: 6 };
    let t: i32 = 0;
    for (k, v) in m { t = t + k + v; }
    return t;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > 4096) { return 1; }
    if (acc != 2200 * 21) { return 98; }
    return 0;
}`, 0},
	{"snapshot/map-keys-loop-break", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let m: Map[i32, i32] = Map { 1: 10, 2: 20, 3: 30 };
        let s: i32 = 0;
        for k in m.keys() { if (k == 2) { break; } s = s + k; }
        if (s != 1) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"snapshot/map-mixed-keys-snapshot", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
        let ks = m.keys();
        m = m.insert(3, "e" + "f");
        if (ks.len() != 2) { bad = 1; }
        if (m.get_or(2, "").len() != 2) { bad = 1; }
        if (m.len() != 3) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"snapshot/map-string-keys-iter-churn-flat", `import "core/map";
function build_iter(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    let seen: i32 = 0;
    for k in m.keys() { seen = seen + k.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    return m.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    let s0: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build_iter(j); j = j + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 2000) { acc = acc + build_noiter(k); k = k + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-string-values-iter-churn-flat", `import "core/map";
function build_iter(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    let seen: i32 = 0;
    for (k, v) in m { seen = seen + v.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    return m.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    let s0: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build_iter(j); j = j + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 2000) { acc = acc + build_noiter(k); k = k + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-kv-iter-churn-flat-wasm", `import "core/map";
function build_it(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4, 5: 6 };
    let t: i32 = 0;
    for (k, v) in m { t = t + k + v; }
    return t;
}
function build_plain(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4, 5: 6 };
    return m.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_it(i) + build_plain(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_it(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_plain(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-i32-grow-churn-wasm", `import "core/map";
function build_grow(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2 };
    let j: i32 = 0;
    while (j < 12) { m = m.insert(j + 10, j * 2); j = j + 1; }
    if (m.has(15)) { return m.len(); }
    return 0;
}
function build_small(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2 };
    return m.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_grow(i) + build_small(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_grow(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_small(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-string-keys-iter-churn-flat-wasm", `import "core/map";
function build_iter(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    let seen: i32 = 0;
    for k in m.keys() { seen = seen + k.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    let m: Map[string, i32] = Map { "a" + "x": 1, "b" + "y": 2, "c" + "z": 3 };
    return m.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    let s0: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_iter(j); j = j + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_noiter(k); k = k + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"snapshot/map-string-values-iter-churn-flat-wasm", `import "core/map";
function build_iter(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    let seen: i32 = 0;
    for (k, v) in m { seen = seen + v.len(); }
    return seen;
}
function build_noiter(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "a", 2: "b" + "b" };
    return m.len();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_iter(i) + build_noiter(i); i = i + 1; }
    let s0: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_iter(j); j = j + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_noiter(k); k = k + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s1 - s0) > (s2 - s1) + 8192) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"struct-key/struct-i32", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function main(): i32 { let m: Map[P, i32] = map_new(8); m = m.insert(P { x: 1, y: 2 }, 10); m = m.insert(P { x: 3, y: 4 }, 20); if (m.get_or(P { x: 1, y: 2 }, 0 - 1) != 10) { return 101; } if (m.get_or(P { x: 3, y: 4 }, 0 - 1) != 20) { return 102; } if (m.get_or(P { x: 9, y: 9 }, 0 - 1) != 0 - 1) { return 103; } if (!m.has(P { x: 1, y: 2 })) { return 104; } if (m.has(P { x: 2, y: 1 })) { return 105; } m = m.insert(P { x: 1, y: 2 }, 99); if (m.len() != 2) { return 106; } return m.get_or(P { x: 1, y: 2 }, 0 - 1); }`, 99},
	{"struct-key/struct-string-field", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct N { first: string, rank: i32 } function main(): i32 { let m: Map[N, i32] = map_new(8); m = m.insert(N { first: "ada", rank: 1 }, 10); m = m.insert(N { first: "bob", rank: 2 }, 20); if (m.get_or(N { first: "a" + "da", rank: 1 }, 0 - 1) != 10) { return 101; } if (m.has(N { first: "ada", rank: 9 })) { return 102; } let (m2, ok) = m.without(N { first: "ada", rank: 1 }); m = m2; if (!ok) { return 103; } if (m.has(N { first: "ada", rank: 1 })) { return 104; } if (m.len() != 1) { return 105; } return m.get_or(N { first: "bob", rank: 2 }, 0 - 1); }`, 20},
	{"struct-key/enum-key-iter", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { A(i32), B, C(string) } function main(): i32 { let m: Map[Tag, i32] = map_new(8); m = m.insert(A(1), 10); m = m.insert(B, 20); m = m.insert(C("x" + "y"), 30); if (m.get_or(C("xy"), 0) != 30) { return 101; } if (m.get_or(A(2), 0 - 1) != 0 - 1) { return 102; } if (m.len() != 3) { return 103; } let t: i32 = 0; for (k, v) in m { t = t + v; } return t; }`, 60},
	{"struct-key/enum-key", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { A(i32), B, C(string) } function main(): i32 { let m: Map[Tag, i32] = map_new(8); m = m.insert(A(1), 10); m = m.insert(B, 20); m = m.insert(C("x" + "y"), 30); if (m.get_or(C("xy"), 0) != 30) { return 101; } if (m.get_or(A(2), 0 - 1) != 0 - 1) { return 102; } if (m.len() != 3) { return 103; } return m.get_or(B, 0) + m.get_or(A(1), 0); }`, 30},
	{"struct-key/struct-grow", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function main(): i32 { let m: Map[P, i32] = map_new(4); let i: i32 = 0; while (i < 20) { m = m.insert(P { x: i, y: i * 2 }, i + 1); i = i + 1; } if (m.len() != 20) { return 101; } let j: i32 = 0; while (j < 20) { if (m.get_or(P { x: j, y: j * 2 }, 0 - 1) != j + 1) { return 1 + j; } j = j + 1; } let (m2, ok) = m.without(P { x: 7, y: 14 }); m = m2; if (!ok) { return 102; } if (m.has(P { x: 7, y: 14 })) { return 103; } if (m.len() != 19) { return 104; } return 42; }`, 42},
	{"values-w64/sum", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 5000000000, 2: 9000000000, 3: 12000000005 }; let vs: i64[] = m.values(); let s: i64 = 0; let i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return (s % 1000) as i32; }`, 5},
	{"values-w64/len", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = Map { 1: 10000000000, 5: 20000000000, 9: 30000000000, 13: 40000000000 }; return m.values().len(); }`, 4},
	{"values-w64/u64-sum-shift", `import "core/map";
function main(): i32 { let m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64, 2: 18000000000000000000 as u64 }; let vs: u64[] = m.values(); let s: u64 = 0; let i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } return (s >> 63) as i32; }`, 1},
	{"values-w64/insert-overwrite", `import "core/map";
function main(): i32 { let m: Map[i32, i64] = map_new(8); m = m.insert(1, 5000000000); m = m.insert(2, 3000000000); m = m.insert(1, 7000000000); let vs: i64[] = m.values(); let s: i64 = 0; let i: i32 = 0; while (i < vs.len()) { s = s + vs[i]; i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return (s % 1000) as i32; }`, 0},
	{"vs-reclaim/mapvs-value-column-flat-arm64", `import "core/map";
function build_str(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}
function build_i32(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(3)) { r = r + 1; }
    return r;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_str(i) + build_i32(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_str(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_i32(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    let str_growth: i32 = s2 - s1;
    let i32_growth: i32 = k2 - s2;
    if (str_growth > i32_growth + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"vs-reclaim/mapvs-value-correct-arm64", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let m: Map[i32, string] = Map { 7: "hel" + "lo", 8: "wor" + "ld" };
        if (m.get_or(7, "").len() != 5) { bad = 1; }
        if (m.get_or(8, "").len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"vs-reclaim/mapvs-aliased-value-excluded-arm64", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 200) {
        let s: string = "aa" + "bb";
        let m: Map[i32, string] = Map { 1: s };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or(1, "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"vs-reclaim/mapvs-value-column-flat", `import "core/map";
function build_str(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}
function build_i32(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    let r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(3)) { r = r + 1; }
    return r;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { acc = acc + build_str(i) + build_i32(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { acc = acc + build_str(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 2000) { acc = acc + build_i32(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    let str_growth: i32 = s2 - s1;
    let i32_growth: i32 = k2 - s2;
    if (str_growth > i32_growth + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"vs-reclaim/mapvs-value-correct", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let m: Map[i32, string] = Map { 7: "hel" + "lo", 8: "wor" + "ld" };
        if (m.get_or(7, "").len() != 5) { bad = 1; }
        if (m.get_or(8, "").len() != 5) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"vs-reclaim/mapvs-aliased-value-excluded", `import "core/map";
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 500) {
        let s: string = "aa" + "bb";
        let m: Map[i32, string] = Map { 1: s };
        if (s.len() != 4) { bad = 1; }
        if (m.get_or(1, "").len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, 0},
	{"vs-reclaim/mapvs-value-column-flat-wasm", `import "core/map";
function build_iv(n: i32): i32 {
    let m: Map[i32, string] = Map { 1: "a" + "b", 2: "c" + "d" };
    return 1;
}
function build_ii(n: i32): i32 {
    let m: Map[i32, i32] = Map { 1: 2, 3: 4 };
    return 1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) { acc = acc + build_iv(i) + build_ii(i); i = i + 1; }
    let s1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 500) { acc = acc + build_iv(j); j = j + 1; }
    let s2: i32 = (__heap_bump_bytes() as i32);
    let k: i32 = 0;
    while (k < 500) { acc = acc + build_ii(k); k = k + 1; }
    let k2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if ((s2 - s1) > (k2 - s2) + 4096) { return 1; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	{"box-key/key-borrowed", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let k: P = P { x: n, y: n * 2 }; let m: Map[P, i32] = map_new(4); m = m.insert(k, 7); let got: i32 = m.get_or(k, 0); return got; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } if (acc != 7700) { return 88; } return 0; }`, 0},
	{"fresh-box-arg/struct-literal-key", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); m = m.insert(P { x: n, y: n * 2 }, 7); return m.len(); } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } if (acc != 1100) { return 88; } return 0; }`, 0},
	{"fresh-box-arg/variant-key", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } function build(n: i32): i32 { let m: Map[Tag, i32] = map_new(4); m = m.insert(Tag.Lo(n), 7); return m.len(); } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } if (acc != 1100) { return 88; } return 0; }`, 0},
	{"fresh-box-arg/variant-value", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } function build(n: i32): i32 { let m: Map[i32, Tag] = map_new(4); m = m.insert(n, Tag.Lo(n)); return m.len(); } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } if (acc != 1100) { return 88; } return 0; }`, 0},
	{"fresh-box-arg/struct-value", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[i32, P] = map_new(4); m = m.insert(n, P { x: n, y: n * 2 }); return m.len(); } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } if (acc != 1100) { return 88; } return 0; }`, 0},
	{"read-key-temp/box-has-fresh", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); let k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); if (m.has(P { x: n, y: n * 2 })) { return 0; } return 0; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/box-has-borrowed", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); let k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); if (m.has(k)) { return 0; } return 0; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/box-get-fresh", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); let k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); let acc: i32 = 0; match (m.get(P { x: n, y: n * 2 })) { Some(v) => { acc = acc + v - 7; }, None => { acc = acc - 1; } } return acc; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/box-get-borrowed", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); let k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); let acc: i32 = 0; match (m.get(k)) { Some(v) => { acc = acc + v - 7; }, None => { acc = acc - 1; } } return acc; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/box-get_or-fresh", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); let k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); return m.get_or(P { x: n, y: n * 2 }, 0) - 7; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/box-get_or-borrowed", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { let m: Map[P, i32] = map_new(4); let k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); return m.get_or(k, 0) - 7; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/variant-get_or-fresh", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } function build(n: i32): i32 { let m: Map[Tag, i32] = map_new(4); let k: Tag = Tag.Lo(n); m = m.insert(k, 7); return m.get_or(Tag.Lo(n), 0) - 7; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"read-key-temp/variant-get_or-borrowed", `import "core/map";
import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } function build(n: i32): i32 { let m: Map[Tag, i32] = map_new(4); let k: Tag = Tag.Lo(n); m = m.insert(k, 7); return m.get_or(k, 0) - 7; } function main(): i32 { let acc: i32 = 0; let w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } let s1: i32 = (__heap_bump_bytes() as i32); let j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } let s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`, 0},
	{"method-tuple-elem/len-elem", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   let u: (i32, i32) = (m.len(), 5);
			   if (u.0 == m.len() && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/has-elem", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   let u: (boolean, boolean) = (m.has("k"), m.has("nope"));
			   if (u.0 && !u.1) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/get_or-i32-elem", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   let u: (i32, i32) = (m.get_or("k", 0), m.get_or("nope", 3));
			   if (u.0 == 7 && u.1 == 3) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/get_or-string-elem", `import "core/map";
function main(): i32 {
			   let m: Map[string, string] = map_new(4);
			   m = m.insert("k", "abcd");
			   let u: (string, i32) = (m.get_or("k", "zz"), 5);
			   if (u.0.len() == 4 && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/get_or-i64-elem", `import "core/map";
function main(): i32 {
			   let m: Map[i32, i64] = __map_new_i32(4);
			   m = m.insert(1, 3000000000i64);
			   let u: (i64, i32) = (m.get_or(1, 0i64), 5);
			   if (u.0 == 3000000000 && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/get-elem", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   let u: (Option[i32], i32) = (m.get("k"), 5);
			   let got: i32 = 0;
			   match (u.0) {
			     Some(v) => { got = v; },
			     None => { got = 0 - 1; },
			   }
			   if (got == 7 && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/len-second-elem", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   m = m.insert("a", 1);
			   m = m.insert("b", 2);
			   let u: (i32, i32) = (5, m.len());
			   if (u.0 == 5 && u.1 == 2) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/arr-len-elem-control", `import "core/map";
function main(): i32 {
			   let xs: i32[] = [1, 2, 3];
			   let u: (i32, i32) = (xs.len(), 5);
			   if (u.0 == 3 && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/keys-elem-control", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   m = m.insert("k", 7);
			   let u: (string[], i32) = (m.keys(), 5);
			   if (u.0.len() == 1 && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"method-tuple-elem/insert-elem-control", `import "core/map";
function main(): i32 {
			   let m: Map[string, i32] = map_new(4);
			   let u: (Map[string, i32], i32) = (m.insert("k", 7), 5);
			   if (u.0.get_or("k", 0) == 7 && u.1 == 5) { return 7; }
			   return 9;
			 }`, 7},
	{"mapiter/iter-cursor", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(4);
    m = m.insert(1, 10);
    m = m.insert(2, 20);
    m = m.insert(3, 30);
    let it: MapIter[i32, i32] = m.iter();
    let sum: i32 = 0;
    while (it.has_next()) {
        sum = sum + it.key() + it.value();
        it.advance();
    }
    return sum;
}`, 66},
	{"struct-field/struct-field", `import "core/map";
struct Cache { m: Map[string, i32], n: i32 }
function f(): i32 {
    let mm: Map[string, i32] = map_new(0);
    mm = mm.insert("a", 3);
    let c: Cache = Cache { m: mm, n: 4 };
    let got: Map[string, i32] = c.m;
    return got.get_or("a", 0) + c.n;
}
function main(): i32 { return f(); }`, 7},
	{"reuse-differential/own-param-donor-map-field", `import "core/map";
struct C { id: i32, m: Map[i32, i32] } function f(own d: C): i32 { let u: i32 = d.id + d.m.len(); let mm: Map[i32, i32] = map_new(4); mm = mm.insert(1, 5); let c = C { id: 10, m: mm }; return c.id + c.m.len() + u; } function main(): i32 { let m0: Map[i32, i32] = map_new(4); m0 = m0.insert(1, 1); return f(C { id: 3, m: m0 }); }`, 15},
	{"reuse-differential/own-param-donor-map-field-call", `import "core/map";
struct C { id: i32, m: Map[i32, i32] } function make_map(): Map[i32, i32] { let mm: Map[i32, i32] = map_new(4); mm = mm.insert(1, 5); return mm; } function f(own d: C): i32 { let u: i32 = d.id + d.m.len(); let c = C { id: 10, m: make_map() }; return c.id + c.m.len() + u; } function main(): i32 { let m0: Map[i32, i32] = map_new(4); m0 = m0.insert(1, 1); return f(C { id: 3, m: m0 }); }`, 15},
	{"reuse-differential/self-overwrite-map-carried-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, id: 2 }; let s: i32 = c.m.get_or(1, 0) + c.id; if (s != 12) { return 99; } return __rc_underflow_count(); }`, 0},
	{"reuse-differential/self-overwrite-map-override", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; return c.m.get_or(1, 0) + c.id; }`, 40},
	{"reuse-differential/self-overwrite-map-override-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let c = P { ...d, m: Map { 1: 39 } }; let s: i32 = c.m.get_or(1, 0) + c.id; if (s != 40) { return 99; } return __rc_underflow_count(); }`, 0},
	{"reuse-differential/cross-struct-map-field-detector", `import "core/map";
struct P { id: i32, m: Map[i32, i32] } function main(): i32 { let d = P { id: 1, m: Map { 1: 10 } }; let u: i32 = d.m.get_or(1, 0) + d.id; let c = P { id: 2, m: Map { 1: 7 } }; let s: i32 = c.m.get_or(1, 0) + c.id + u; if (s != 20) { return 99; } return __rc_underflow_count(); }`, 0},
	{"ir-check-gate/literal-desugar", `import "core/map";
function main(): i32 { let m: Map[i32, i32] = Map { 1: 40, 2: 2 }; return m.get_or(1, 0) + m.get_or(2, 0); }`, 42},
}

func TestSelfHostMapPrograms(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, tc := range mapProgramCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				if stderr, code := cli.exitOf(t, tc.src, target); code != tc.want {
					t.Errorf("exited %d, want %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
