package e2ecompiler

import (
	"testing"
)

var mapFixpointIRCases = []struct {
	name  string
	src   func(n string) string
	fixed bool
	want  int
}{
	{name: "cow-loop-getor", src: func(n string) string {
		return `import "core/map";
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) {
        let m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "straightline-cow-getor", src: func(n string) string {
		return `import "core/map";
function step2(k: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m.get_or(k, 0) + m.get_or(k + 1, 0);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step2(i); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "per-call-getor", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m.get_or(k, 7);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "getor-miss-path", src: func(n string) string {
		return `import "core/map";
function step(k: i32): i32 {
    let m: Map[i32, i32] = map_new(8);
    return m.get_or(k, 7);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc != ` + n + ` * 7) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "mkcall-loop-getor", src: func(n string) string {
		return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) {
        let m: Map[i32, i32] = mk(i);
        acc = acc + m.get_or(i, 0);
        i = i + 1;
    }
    if (acc != ` + n + ` * (` + n + ` - 1)) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "mkcall-straightline", src: func(n string) string {
		return `import "core/map";
function mk2(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    m = m.insert(k + 1, k * 3);
    return m;
}
function step(k: i32): i32 {
    let m: Map[i32, i32] = mk2(k);
    return m.get_or(k, 0) + m.get_or(k + 1, 0);
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + step(i); i = i + 1; }
    if (acc < 0) { return 121; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "mkcall-discard", src: func(n string) string {
		return `import "core/map";
function mk(k: i32): Map[i32, i32] {
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { mk(i); i = i + 1; }
    let g: i32 = (__heap_bump_bytes() as i32) - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	{name: "value-churn", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 300) {
        let m: Map[i32, i32] = map_new(8);
        m = m.insert(i, i * 2);
        m = m.insert(i + 1, i * 3);
        acc = acc + m.get_or(i, 0) + m.get_or(i + 1, 0);
        i = i + 1;
    }
    if (acc != 5 * (300 * 299 / 2)) { return 121; }
    return 0;
}`
	}},
	{name: "alias-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = map_new(8);
    let x: Map[i32, i32] = m.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { acc = acc + x.get_or(1, 0) + m.get_or(1, 0); i = i + 1; }
    if (acc != 200 * 11) { return 121; }
    return 0;
}`
	}},
	{name: "mkcall-param-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function feed(m: Map[i32, i32]): Map[i32, i32] {
    return m;
}
function grow(m: Map[i32, i32], k: i32): Map[i32, i32] {
    let t: Map[i32, i32] = m.insert(k, k * 2);
    return t;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let x: Map[i32, i32] = feed(base);
        let y: Map[i32, i32] = grow(base, i + 2);
        acc = acc + x.get_or(1, 0) + y.get_or(1, 0);
        i = i + 1;
    }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
	{name: "discard-param-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function feed(m: Map[i32, i32]): Map[i32, i32] {
    return m;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { feed(base); acc = acc + base.get_or(1, 0); i = i + 1; }
    if (acc != 200 * 11) { return 121; }
    return 0;
}`
	}},
	{name: "mkcall-nested-return-negative", fixed: true, want: 0, src: func(string) string {
		return `import "core/map";
function pick(p: Map[i32, i32], k: i32): Map[i32, i32] {
    if (k < 0) { return p; }
    let m: Map[i32, i32] = map_new(8);
    m = m.insert(k, k * 2);
    return m;
}
function main(): i32 {
    let base: Map[i32, i32] = map_new(8);
    base = base.insert(1, 11);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let m: Map[i32, i32] = pick(base, 0 - 1);
        acc = acc + m.get_or(1, 0) + base.get_or(1, 0);
        i = i + 1;
    }
    if (acc != 200 * 22) { return 121; }
    return 0;
}`
	}},
}

func TestSelfHostMapFixpointIRX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	sh := func(t *testing.T, prog string) int {
		t.Helper()
		_, exit := cli.exitOf(t, prog+"\n", "x86-64-linux")
		return exit
	}

	for _, tc := range mapFixpointIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixed {
				code := sh(t, tc.src(""))
				if code != tc.want {
					t.Errorf("%s: exited %d, want %d (121=value mismatch, 119=leak guard)", tc.name, code, tc.want)
				}
				if native := runInterpExit(t, tc.src("")); native != code {
					t.Errorf("%s: differential mismatch — native -interp exited %d, self-host exited %d", tc.name, native, code)
				}
				return
			}
			small := sh(t, tc.src("50"))
			large := sh(t, tc.src("5000"))
			if small != large {
				t.Errorf("%s: high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: growth is 0 — probe does not allocate", tc.name)
			}
			if small >= 119 {
				t.Errorf("%s: leak guard tripped (%d)", tc.name, small)
			}
		})
	}
}
