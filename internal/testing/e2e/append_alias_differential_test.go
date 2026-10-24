package e2e

import "testing"

func TestAppendAliasDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping append-alias differential in -short mode")
	}
	cases := []struct {
		name, src string
	}{
		{"param_arg_position_reused", `import "std/i32";
function walk(path: i32[], depth: i32): i32 {
    if (depth == 0) { return path.len(); }
    let a: i32 = walk(path.append(depth), depth - 1);
    let b: i32 = path.append(depth).len();
    return a * 100 + b;
}
function main(): i32 {
    let p: i32[] = [];
    print(walk(p, 2).to_string());
    return 0;
}`},
		{"param_arg_position_reused_depth3", `import "std/i32";
function walk(path: i32[], depth: i32): i32 {
    if (depth == 0) { return path.len(); }
    let a: i32 = walk(path.append(depth), depth - 1);
    let b: i32 = path.append(depth).len();
    return a * 1000 + b;
}
function main(): i32 {
    let p: i32[] = [];
    print(walk(p, 3).to_string());
    return 0;
}`},
		{"local_ident_reused", `import "std/i32";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let a: i32 = xs.append(9).len();   // 4
    let b: i32 = xs.append(8).len();   // must still be 4, not 5
    let c: i32 = xs.len();             // must still be 3
    print((a * 100 + b * 10 + c).to_string());
    return 0;
}`},
		{"ident_append_then_read", `import "std/i32";
function main(): i32 {
    let xs: i32[] = [4, 5];
    let a: i32 = xs.append(6).len();   // 3
    let b: i32 = xs.len();             // 2, not 3
    print((a * 10 + b).to_string());
    return 0;
}`},
		{"self_reassign_push_loop", `import "std/i32";
function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 8) { a = a.append(i * i); i = i + 1; }
    let s: i32 = 0;
    let j: i32 = 0;
    while (j < a.len()) { s = s + a[j]; j = j + 1; }
    print((s * 10 + a.len()).to_string());
    return 0;
}`},
		{"fresh_temporary_chain", `import "std/i32";
function main(): i32 {
    let n: i32 = [1, 2, 3].append(4).append(5).append(6).len();  // 6
    print(n.to_string());
    return 0;
}`},
		{"return_position_accumulator", `import "std/i32";
function collect(acc: i32[], v: i32): i32[] {
    return acc.append(v * 3);
}
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 6) { acc = collect(acc, i); i = i + 1; }
    let s: i32 = 0;
    let j: i32 = 0;
    while (j < acc.len()) { s = s + acc[j]; j = j + 1; }
    print((s * 10 + acc.len()).to_string());
    return 0;
}`},
		{"param_self_reassign_threaded", `import "std/i32";
function extend(xs: i32[], n: i32): i32[] {
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}
function main(): i32 {
    let a: i32[] = extend([], 4);
    a = extend(a, 3);
    let s: i32 = 0;
    let j: i32 = 0;
    while (j < a.len()) { s = s + a[j]; j = j + 1; }
    print((s * 10 + a.len()).to_string());
    return 0;
}`},
		{"grown_buffer_two_appends", `import "std/i32";
function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let x: i32[] = a.append(100);
    let y: i32[] = a.append(200);
    print((x[5] + y[5] * 10 + a.len() * 100).to_string());
    return 0;
}`},
		{"grown_buffer_append_through_field", `import "std/i32";
struct Box { items: i32[] }
function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let b: Box = Box { items: a };
    let x: i32[] = b.items.append(100);
    let y: i32[] = a.append(200);
    print((x[5] + y[5] * 10 + b.items.len() * 100).to_string());
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumProgramAgrees(t, tc.src)
		})
	}
}

const appendCopyLeakBoundProgram = `function take(xs: i32[]): i32 { return xs.len(); }

function main(): i32 {
    let path: i32[] = [1, 2];
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { acc = acc + take(path.append(w)); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { acc = acc + take(path.append(i)); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (path.len() != 2) { return 97; }
    if (acc < 0) { return 96; }
    return 0;
}
`

const builtinResultArgLeakBoundProgram = `function take(s: string): i32 { return s.len(); }

function main(): i32 {
    let h: usize = buf_new(64);
    let acc: i32 = 0;
    let w: i32 = 0;
    while (w < 200) { buf_push(h, "twelve bytes"); acc = acc + take(buf_take(h)); w = w + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < 5000) { buf_push(h, "twelve bytes"); acc = acc + take(buf_take(h)); i = i + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    buf_free(h);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc != 5200 * 12) { return 96; }
    return 0;
}
`

func TestX86_64BuiltinResultArgLeakBound(t *testing.T) {
	if _, code := compileAndRunX86_64(t, builtinResultArgLeakBoundProgram); code != 0 {
		t.Errorf("x86-64 builtin-result argument leak bound: exit = %d, want 0 (98 = take leaked; 99 = over-release)", code)
	}
}

func TestArm64BuiltinResultArgLeakBound(t *testing.T) {
	if _, code := compileAndRunArm64(t, builtinResultArgLeakBoundProgram); code != 0 {
		t.Errorf("arm64 builtin-result argument leak bound: exit = %d, want 0 (98 = take leaked; 99 = over-release)", code)
	}
}

func TestWASMBuiltinResultArgLeakBound(t *testing.T) {
	if got := runWasm(t, builtinResultArgLeakBoundProgram); got != 0 {
		t.Errorf("wasm builtin-result argument leak bound: exit = %d, want 0 (98 = take leaked; 99 = over-release)", got)
	}
}

func TestX86_64AppendCopyLeakBound(t *testing.T) {
	if _, code := compileAndRunX86_64(t, appendCopyLeakBoundProgram); code != 0 {
		t.Errorf("x86-64 append-copy leak bound: exit = %d, want 0 (98 = copy leaked; 99 = over-release; 97 = operand mutated)", code)
	}
}
