package e2e

import (
	"os/exec"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var freelistReuseSrc = struct{ reuse, wrongClass, lifo string }{
	reuse: `
function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`,
	wrongClass: `
function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`,
	lifo: `
function main(): i32 {
    let a: usize = __alloc(48);
    let b: usize = __alloc(48);
    __free(a, 48);
    __free(b, 48);
    let c: usize = __alloc(48);
    let d: usize = __alloc(48);
    if (c == b) {
        if (d == a) { return 0; }
        return 1;
    }
    return 2;
}`,
}

func compileAndRunX86_64FreeOn(t *testing.T, src string) (string, int) {
	t.Helper()
	binPath, runner := compileX86_64FreeOn(t, src)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func compileX86_64FreeOn(t *testing.T, src string) (string, []string) {
	t.Helper()
	runner := e2eharness.X86_64Runner(t)
	return e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, src, nil), runner
}

func compileAndRunArm64FreeOn(t *testing.T, src string) (string, int) {
	t.Helper()
	return compileAndRunArm64FreeOnArgs(t, src)
}

func compileAndRunArm64FreeOnArgs(t *testing.T, src string, args ...string) (string, int) {
	t.Helper()
	binPath, qemu := compileArm64FreeOn(t, src)
	cmd := runArm64Bin(qemu, binPath, args...)
	out, _ := cmd.CombinedOutput()
	_ = out
	return "", cmd.ProcessState.ExitCode()
}

func compileArm64FreeOn(t *testing.T, src string) (string, string) {
	t.Helper()
	qemu := e2eharness.Arm64Runner(t)
	return e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, src, nil), qemu
}

func TestX86_64FreelistReuse(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, freelistReuseSrc.reuse); code != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, freelistReuseSrc.wrongClass); code != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, freelistReuseSrc.lifo); code != 0 {
		t.Errorf("LIFO reuse: got %d, want 0 (c==b, d==a)", code)
	}
}

const arrayDropFreeReuseSrc = `struct Foo { v: i32 }
function consume(n: i32): i32 {
    let fs: Foo[] = [Foo { v: n }, Foo { v: n + 1 }, Foo { v: n + 2 }];
    return fs[2].v;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        acc = acc + (consume(i) - (i + 2));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`

const pushLoopFreeSrc = `function build(): i32 {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < 200) {
        xs = xs.append(i);
        i = i + 1;
    }
    let sum: i32 = 0;
    let j: i32 = 0;
    while (j < xs.len()) {
        sum = sum + xs[j];
        j = j + 1;
    }
    return sum;
}
function main(): i32 {
    return (build() - 19900) + __rc_underflow_count();
}`

const stringReassignFreeSrc = `function build(): i32 {
    let s: string = "";
    let i: i32 = 0;
    while (i < 300) {
        s = s + "x";
        i = i + 1;
    }
    return s.len();
}
function aliasHeap(w: string): i32 {
    let a: string = "a" + w;
    let b: string = "bb" + w;
    a = b;
    return a.len() + b.len();
}
function main(): i32 {
    let grown: i32 = build() - 300;
    let aliased: i32 = aliasHeap("zz") - 8;
    return grown + aliased + __rc_underflow_count();
}`

func TestX86_64ArrayDropFree(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, arrayDropFreeReuseSrc); code != 0 {
		t.Errorf("drop+free+reuse: got %d, want 0 (a corrupted reuse or over-release would drift)", code)
	}
	if _, code := compileAndRunX86_64FreeOn(t, pushLoopFreeSrc); code != 0 {
		t.Errorf("push-loop free+reuse: got %d, want 0", code)
	}
	for _, c := range rcCorpusCases(t) {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption when blocks are freed+reused)", c.name, code)
			}
		})
	}
}

func TestArm64ArrayDropFree(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, arrayDropFreeReuseSrc); code != 0 {
		t.Errorf("drop+free+reuse: got %d, want 0", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, pushLoopFreeSrc); code != 0 {
		t.Errorf("push-loop free+reuse: got %d, want 0", code)
	}
	for _, c := range rcCorpusCases(t) {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption)", c.name, code)
			}
		})
	}
}

func TestWASMArrayDropFree(t *testing.T) {
	if got := runWasm(t, arrayDropFreeReuseSrc); got != 0 {
		t.Errorf("drop+free+reuse: got %d, want 0", got)
	}
	if got := runWasm(t, pushLoopFreeSrc); got != 0 {
		t.Errorf("push-loop free+reuse: got %d, want 0", got)
	}
	for _, c := range rcCorpusCases(t) {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s (free-on): got %d, want 0 (UAF / corruption)", c.name, got)
			}
		})
	}
}

func TestX86_64StringReassignFree(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, stringReassignFreeSrc); code != 0 {
		t.Errorf("string reassign free+reuse: got %d, want 0 (drift / over-release on string dec-on-overwrite)", code)
	}
}

func TestWASMStringReassignFree(t *testing.T) {
	if got := runWasm(t, stringReassignFreeSrc); got != 0 {
		t.Errorf("string reassign free+reuse: got %d, want 0 (drift / over-release on string dec-on-overwrite)", got)
	}
}

func TestWASMFreelistReuse(t *testing.T) {

	reuse := `function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(64);
    if (a == b) { return 0; }
    return 1;
}`
	if got := runWasm(t, reuse); got != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", got)
	}

	wrongClass := `function main(): i32 {
    let a: usize = __alloc(64);
    __free(a, 64);
    let b: usize = __alloc(32);
    if (a == b) { return 1; }
    return 0;
}`
	if got := runWasm(t, wrongClass); got != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", got)
	}

	lifo := `function main(): i32 {
    let a: usize = __alloc(48);
    let b: usize = __alloc(48);
    __free(a, 48);
    __free(b, 48);
    let c: usize = __alloc(48);
    let d: usize = __alloc(48);
    if (c == b) {
        if (d == a) { return 0; }
        return 1;
    }
    return 2;
}`
	if got := runWasm(t, lifo); got != 0 {
		t.Errorf("LIFO reuse: got %d, want 0 (c==b, d==a)", got)
	}
}

func TestArm64FreelistReuse(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, freelistReuseSrc.reuse); code != 0 {
		t.Errorf("same-size reuse: got %d, want 0 (freed block should be reused)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, freelistReuseSrc.wrongClass); code != 0 {
		t.Errorf("wrong-class reuse: got %d, want 0 (32-byte alloc must not reuse a 64-byte free)", code)
	}
	if _, code := compileAndRunArm64FreeOn(t, freelistReuseSrc.lifo); code != 0 {
		t.Errorf("LIFO reuse: got %d, want 0 (c==b, d==a)", code)
	}
}

var structReuseSrc = struct{ churn, aliased, swap string }{
	churn: `struct Point { x: i32, y: i32 }
function churn(n: i32): i32 {
    let p: Point = Point { x: 0, y: 0 };
    let i: i32 = 0;
    while (i < n) {
        p = Point { x: p.x + 1, y: p.y };
        i = i + 1;
    }
    return p.x;
}
function main(): i32 {
    return (churn(200) - 200) + __rc_underflow_count();
}`,
	aliased: `struct Point { x: i32, y: i32 }
function main(): i32 {
    let p: Point = Point { x: 5, y: 7 };
    let q: Point = p;
    p = Point { x: p.x + 1, y: p.y };
    if (q.x != 5) { return 1; }
    if (p.x != 6) { return 2; }
    return __rc_underflow_count();
}`,
	swap: `struct Pair { a: i32, b: i32 }
function churn(n: i32): i32 {
    let p: Pair = Pair { a: 1, b: 2 };
    let i: i32 = 0;
    while (i < n) {
        p = Pair { a: p.b, b: p.a };
        i = i + 1;
    }
    return p.a;
}
function main(): i32 {
    return (churn(200) - 1) + (churn(201) - 2) + __rc_underflow_count();
}`,
}

var structPtrReuseSrc = struct{ carried, aliased, replaced string }{
	carried: `struct Holder { id: i32, items: i32[] }
function churn(n: i32): i32 {
    let p: Holder = Holder { id: 0, items: [10, 20, 30] };
    let i: i32 = 0;
    while (i < n) {
        p = Holder { id: p.id + 1, items: p.items };
        i = i + 1;
    }
    return (p.id - n) + (p.items[0] + p.items[1] + p.items[2] - 60);
}
function main(): i32 {
    return churn(200) + __rc_underflow_count();
}`,
	aliased: `struct Holder { id: i32, items: i32[] }
function main(): i32 {
    let p: Holder = Holder { id: 1, items: [7, 8] };
    let q: Holder = p;
    p = Holder { id: p.id + 1, items: p.items };
    if (q.id != 1) { return 1; }
    if (q.items[0] != 7) { return 2; }
    if (p.id != 2) { return 3; }
    if (p.items[1] != 8) { return 4; }
    return __rc_underflow_count();
}`,
	replaced: `struct Holder { id: i32, items: i32[] }
function churn(n: i32): i32 {
    let p: Holder = Holder { id: 0, items: [0] };
    let i: i32 = 0;
    while (i < n) {
        p = Holder { id: p.id + 1, items: [p.id + 1, p.id + 1] };
        i = i + 1;
    }
    return (p.id - n) + (p.items[0] - n) + (p.items[1] - n);
}
function main(): i32 {
    return churn(100) + __rc_underflow_count();
}`,
}

var structReuseCases = []struct{ name, src string }{
	{"churn", structReuseSrc.churn},
	{"aliased", structReuseSrc.aliased},
	{"swap", structReuseSrc.swap},
	{"ptr_carried", structPtrReuseSrc.carried},
	{"ptr_aliased", structPtrReuseSrc.aliased},
	{"ptr_replaced", structPtrReuseSrc.replaced},
}

func TestX86_64StructReuse(t *testing.T) {
	for _, c := range structReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64StructReuse(t *testing.T) {
	for _, c := range structReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMStructReuse(t *testing.T) {
	for _, c := range structReuseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}

var moveOnConstructionCases = []struct{ name, src string }{
	{"once", `struct Wrap { inner: i32[] }
function build(): i32 {
    let x: i32[] = [10, 20, 30];
    let s: Wrap = Wrap { inner: x };
    return s.inner[0] + s.inner[1] + s.inner[2];
}
function main(): i32 {
    return (build() - 60) + __rc_underflow_count();
}`},
	{"churn", `struct Wrap { inner: i32[] }
function once(n: i32): i32 {
    let x: i32[] = [n, n + 1];
    let s: Wrap = Wrap { inner: x };
    return s.inner[0] + s.inner[1];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (2 * i + 1));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	{"array_elem", `function once(n: i32): i32 {
    let x: i32[] = [n, n + 1];
    let xs: i32[][] = [x];
    return xs[0][0] + xs[0][1];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (2 * i + 1));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	{"tuple_elem", `function once(n: i32): i32 {
    let x: i32[] = [n, n + 2];
    let t: (i32[], i32) = (x, n);
    return t.0[0] + t.0[1] + t.1;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (3 * i + 2));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	{"closure_capture", `function once(n: i32): i32 {
    let x: i32[] = [n, n + 5];
    function get(): i32 { return x[0] + x[1]; }
    return get();
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (2 * i + 5));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
	{"returned", `struct Wrap { inner: i32[] }
function build(n: i32): Wrap {
    let x: i32[] = [n, n + 1, n + 2];
    let s: Wrap = Wrap { inner: x };
    return s;
}
function main(): i32 {
    let w: Wrap = build(5);
    return (w.inner[0] + w.inner[1] + w.inner[2] - 18) + __rc_underflow_count();
}`},
	{"destructure", `function once(n: i32): i32 {
    let t: (i32[], i32) = ([n, n + 3], n + 7);
    let (a, b) = t;
    return a[0] + a[1] + b;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        acc = acc + (once(i) - (3 * i + 10));
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`},
}

func TestX86_64MoveOnConstruction(t *testing.T) {
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestArm64MoveOnConstruction(t *testing.T) {
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64FreeOn(t, c.src); code != 0 {
				t.Errorf("%s: got %d, want 0", c.name, code)
			}
		})
	}
}

func TestWASMMoveOnConstruction(t *testing.T) {
	for _, c := range moveOnConstructionCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runWasm(t, c.src); got != 0 {
				t.Errorf("%s: got %d, want 0", c.name, got)
			}
		})
	}
}
