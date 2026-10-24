package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const spillSlotMatesProg = `@noinline function carry(n: i64, x: i64): i64 {
    let a: i64 = 1i64; let b: i64 = 2i64; let c: i64 = 3i64; let d: i64 = 4i64;
    let e: i64 = 5i64; let f: i64 = 6i64; let g: i64 = 7i64; let h: i64 = 8i64;
    let p: i64 = 9i64; let q: i64 = 10i64; let r: i64 = 11i64; let s: i64 = 12i64;
    let t: i64 = 13i64; let u: i64 = 14i64; let w: i64 = 15i64; let y: i64 = 16i64;
    let i: i64 = 0i64;
    while (i < n) {
        a = a + x; b = b + a; c = c + b; d = d + c; e = e + d; f = f + e; g = g + f; h = h + g;
        p = p + h; q = q + p; r = r + q; s = s + r; t = t + s; u = u + t; w = w + u; y = y + w;
        i = i + 1i64;
    }
    return a + b + c + d + e + f + g + h + p + q + r + s + t + u + w + y;
}
function main(): i32 { return (carry(1000i64, 3i64) % 200i64) as i32; }
`

const spillSlotNestedProg = `@noinline function flush(x: i64): i64 { return x * 3i64 + 1i64; }
@noinline function carry(n: i64, x: i64): i64 {
    let a: i64 = 1i64; let b: i64 = 2i64; let c: i64 = 3i64; let d: i64 = 4i64;
    let e: i64 = 5i64; let f: i64 = 6i64; let g: i64 = 7i64; let h: i64 = 8i64;
    let p: i64 = 9i64; let q: i64 = 10i64; let r: i64 = 11i64; let s: i64 = 12i64;
    let t: i64 = 13i64; let u: i64 = 14i64; let w: i64 = 15i64; let y: i64 = 16i64;
    let i: i64 = 0i64;
    let acc: i64 = 0i64;
    while (i < n) {
        acc = acc + x;
        while (acc > 1000i64) {
            a = flush(a); b = flush(b); c = flush(c); d = flush(d); e = flush(e); f = flush(f); g = flush(g); h = flush(h);
            p = flush(p); q = flush(q); r = flush(r); s = flush(s); t = flush(t); u = flush(u); w = flush(w); y = flush(y);
            acc = acc - 1000i64;
        }
        i = i + 1i64;
    }
    return a + b + c + d + e + f + g + h + p + q + r + s + t + u + w + y + acc;
}
function main(): i32 { return (carry(1000i64, 3i64) % 200i64) as i32; }
`

const spillSlotJoinProg = `struct W { f0: i64[], f1: i64[], f2: i64[], f3: i64[], f4: i64[], f5: i64[], f6: i64[], f7: i64[], f8: i64[], f9: i64[], f10: i64[], f11: i64[], f12: i64[], f13: i64[] }
@noinline function rare(w: W): W { return w; }
@noinline function carry(n: i32, xs: i64[]): i32 {
    let v0: i64[] = xs;
    let v1: i64[] = xs;
    let v2: i64[] = xs;
    let v3: i64[] = xs;
    let v4: i64[] = xs;
    let v5: i64[] = xs;
    let v6: i64[] = xs;
    let v7: i64[] = xs;
    let v8: i64[] = xs;
    let v9: i64[] = xs;
    let v10: i64[] = xs;
    let v11: i64[] = xs;
    let v12: i64[] = xs;
    let v13: i64[] = xs;
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < n) {
        while (acc > 1000000) {
            let w1: W = rare(W { f0: v0, f1: v1, f2: v2, f3: v3, f4: v4, f5: v5, f6: v6, f7: v7, f8: v8, f9: v9, f10: v10, f11: v11, f12: v12, f13: v13 });
            v0 = w1.f0; v1 = w1.f1; v2 = w1.f2; v3 = w1.f3; v4 = w1.f4; v5 = w1.f5; v6 = w1.f6; v7 = w1.f7; v8 = w1.f8; v9 = w1.f9; v10 = w1.f10; v11 = w1.f11; v12 = w1.f12; v13 = w1.f13;
            acc = acc - 1;
        }
        acc = acc + v13.len() + i;
        if (acc > 2000000) {
            let w2: W = rare(W { f0: v0, f1: v1, f2: v2, f3: v3, f4: v4, f5: v5, f6: v6, f7: v7, f8: v8, f9: v9, f10: v10, f11: v11, f12: v12, f13: v13 });
            v0 = w2.f0; v1 = w2.f1; v2 = w2.f2; v3 = w2.f3; v4 = w2.f4; v5 = w2.f5; v6 = w2.f6; v7 = w2.f7; v8 = w2.f8; v9 = w2.f9; v10 = w2.f10; v11 = w2.f11; v12 = w2.f12; v13 = w2.f13;
        }
        i = i + 1;
    }
    return acc + v0.len() + v1.len() + v2.len() + v3.len() + v4.len() + v5.len() + v6.len() + v7.len() + v8.len() + v9.len() + v10.len() + v11.len() + v12.len() + v13.len();
}
function main(): i32 { let xs: i64[] = [1i64, 2i64]; return carry(1000, xs) % 200; }
`

func TestSelfHostSpillSlotMates(t *testing.T) {
	t.Run("carried", func(t *testing.T) { checkSpillSlotMates(t, spillSlotMatesProg, 164) })
	t.Run("nested", func(t *testing.T) { checkSpillSlotMates(t, spillSlotNestedProg, 88) })
	t.Run("merges", func(t *testing.T) { checkSpillSlotMates(t, mergeHintProg, 86) })
	t.Run("join", func(t *testing.T) { checkSkippedArmMovesNothing(t, spillSlotJoinProg, 128) })
}

func checkSkippedArmMovesNothing(t *testing.T, prog string, want int) {
	body := spillSlotListing(t, prog, want)
	skip := regexp.MustCompile(`cmpq \$2000000, \S+\n\s*jle (\S+)\n`).FindStringSubmatch(body)
	if skip == nil {
		t.Fatalf("no skip of the rare arm in:\n%s", body)
	}
	at := strings.Index(body, "\n"+skip[1]+":\n")
	if at < 0 {
		t.Fatalf("%s is not in the listing", skip[1])
	}
	path := body[at+1:]
	if end := regexp.MustCompile(`\n\s*j[a-z]+ `).FindStringIndex(path); end != nil {
		path = path[:end[0]]
	}
	if regexp.MustCompile(`movq -?\d+\(%rbp\), %rcx\n\s*movq %rcx, -?\d+\(%rbp\)`).MatchString(path) {
		t.Errorf("the turn that skips the rare arm copies slots:\n%s", path)
	}
}

func checkSpillSlotMates(t *testing.T, prog string, want int) {
	body := spillSlotListing(t, prog, want)
	lines := strings.Split(body, "\n")
	load := regexp.MustCompile(`^\s*movq (-?\d+)\(%rbp\), %rcx$`)
	store := regexp.MustCompile(`^\s*movq %rcx, (-?\d+)\(%rbp\)$`)
	slot := regexp.MustCompile(`^\s*movq .*-\d+\(%rbp\)`)
	spills := 0
	for i, l := range lines {
		if slot.MatchString(l) {
			spills++
		}
		if i+1 < len(lines) && load.MatchString(l) && store.MatchString(lines[i+1]) {
			t.Errorf("carry copies one slot to another:\n%s\n%s\n\n%s", l, lines[i+1], body)
		}
	}
	if spills == 0 {
		t.Fatalf("carry spills nothing, so the absence of slot copies proves nothing:\n%s", body)
	}
}

func spillSlotListing(t *testing.T, prog string, want int) string {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "carry.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "carry.s")
	if combined, err := exec.Command(h.cli, "-O", "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)\n__fn_carry\.r:\n(.*?)\n__fn_`).FindStringSubmatch(string(asm))
	if m == nil {
		t.Fatal("__fn_carry.r not in the listing")
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != want {
			t.Errorf("%s: exit %d, want %d (the interpreter's)", tg.target, got, want)
		}
	}
	return m[1]
}
