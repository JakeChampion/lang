package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// arrPushCliffIRCases pin `__arr_push_shared_count()` — the rc==1 append cliff
// counter — on the self-host IR path. `__fern_arr_push` mutates in place only
// at rc == 1, so that threshold is a performance-correctness boundary with no
// diagnostic of its own: one stray retain upstream makes every append in a
// threaded accumulator copy the whole buffer, and the program stays CORRECT
// while going quadratic.
//
// BOTH halves are pinned, and the healthy case alone is not enough: the
// self-host lowering has to know the builtin, and a reader wired up without a
// bump site returns 0 forever — which reads as a clean run rather than as
// missing instrumentation. The shared case is what proves the
// counter can fire.
//
// The rows are the oracle: the interpreter has no refcounts and copies
// nothing, so it reports 0 for both and cannot judge the counter. Exit codes
// stay well under the wasmtime clamp.
var arrPushCliffIRCases = []struct {
	name string
	main string
	want int
}{
	// A threaded accumulator handed back through a borrowed param — the shape
	// every byte-emitter in the self-host compiler is built from. Nothing else
	// holds the buffer, so every append after a grow mutates in place.
	{"healthy-threaded-accumulator", `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 200) { acc = step(acc, i); i = i + 1; }
    if (acc.len() != 200) { return 254; }
    if (acc[7] != 7 || acc[199] != 199) { return 253; }
    return __arr_push_shared_count();
}`, 0},
	// Crosses the cliff exactly once, deliberately. The loop leaves the buffer
	// with spare capacity; `b` then takes a second reference, so the append
	// that follows cannot mutate in place despite the room and must copy.
	// Reading both afterwards proves the copy really happened — had the append
	// mutated in place, b would see the longer length.
	{"shared-buffer-with-spare-capacity", `function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let b: i32[] = a;
    let c: i32[] = a.append(99);
    if (b.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 99 || b[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
	// The accumulator handed in as a PLAIN PARAMETER and appended to in a
	// LOOP — coreutils/echo.fern's `append_raw`, and neither of the two shapes
	// the tail-form case above covers. The self-host used to un-share this one
	// ITSELF, with an `__fern_arr_slice` ahead of the consuming push, so the
	// copy never reached `__fern_arr_push`'s own shared-cliff path: the count
	// read 0 while the program copied the whole accumulator per call, which is
	// the reading this counter exists to make impossible (#9526).
	{"shared-param-accumulator-appended-in-a-loop", `function chunk(out: i32[], s: i32[]): i32[] {
    let bs: i32[] = out;
    let i: i32 = 0;
    while (i < s.len()) { bs = bs.append(s[i]); i = i + 1; }
    return bs;
}
function main(): i32 {
    let a: i32[] = [];
    let i: i32 = 0;
    while (i < 5) { a = a.append(i); i = i + 1; }
    let s: i32[] = [];
    s = s.append(9);
    let keep: i32[] = a;
    let c: i32[] = chunk(a, s);
    if (keep.len() != 5 || c.len() != 6) { return 250; }
    if (c[5] != 9 || keep[4] != 4) { return 251; }
    return __arr_push_shared_count();
}`, 1},
}

// TestSelfHostArrPushCliffIR runs each case through the self-host CLI on
// x86-64 and wasm against each row's expected count. Wasm keeps the counter in
// a fixed low-memory slot (`arr_push_shared_addr`) instead of a BSS word; the
// interpreter has no cliff counter, so the rows are the oracle.
func TestSelfHostArrPushCliffIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrPushCliffIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.main + "\n"
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}

// The reference count is a 32-bit word at [data-8]; the four bytes above it
// belong to the box's previous life. A block recycled from the allocator keeps
// whatever its last owner left there — here the string box's data pointer,
// which `string_from_bytes_unchecked` lays over the same word — so a push
// that read the count as 64 bits saw every recycled receiver as shared and
// copied it on every append until the next doubling moved to a fresh block.
// ptx paid 35 GB of copies for that on 120k words (#9083); the byte blocks are
// the size of the 32,768-capacity array box, so the fill's doubling lands on
// one of them.
const arrPushRecycledProg = `function fill(n: i32): i64[] {
    let out: i64[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append(0 as i64); i = i + 1; }
    return out;
}
function churn(k: i32): i32 {
    let total: i32 = 0;
    let j: i32 = 0;
    while (j < k) {
        let b: u8[] = __alloc_u8(262152);
        b = b.with(0, 255 as u8);
        let s: string = string_from_bytes_unchecked(b);
        total = total + s.len() % 3;
        j = j + 1;
    }
    return total;
}
function main(): i32 {
    let before: i32 = __arr_push_shared_count();
    let c: i32 = churn(8);
    let xs: i64[] = fill(40000);
    if (xs.len() != 40000 || c < 0) { return 250; }
    if (__arr_push_shared_count() != before) { return 1; }
    return 0;
}
`

// TestSelfHostArrPushRecycledBlockIsNotShared runs the program above on every
// target the self-host CLI emits for: the count must stay where it was, so
// the exit code is 0 rather than 1.
func TestSelfHostArrPushRecycledBlockIsNotShared(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, arrPushRecycledProg, target); code != 0 {
				t.Errorf("%s exited %d, want 0: a recycled block's receiver is taken as shared\n%s", target, code, stderr)
			}
		})
	}
}

// TestSelfHostArrPushRecycledBlockIsNotSharedOnHost runs the same program
// natively on the host the suite runs on, which is how the Darwin runtime is
// reached and how the case runs with no cross tooling at all.
func TestSelfHostArrPushRecycledBlockIsNotSharedOnHost(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "recycled.fern")
	if err := os.WriteFile(src, []byte(arrPushRecycledProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 0 {
			t.Errorf("%s: exit %d, want 0: a recycled block's receiver is taken as shared", tg.target, got)
		}
	}
}

// TestSelfHostArrPushReadsCountAsWord pins the instruction: every rc read in
// the push helpers and their inlined heads is the 32-bit form the box writes,
// never a 64-bit load of the word.
func TestSelfHostArrPushReadsCountAsWord(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "push.fern")
	if err := os.WriteFile(src, []byte(arrPushRecycledProg), 0o644); err != nil {
		t.Fatal(err)
	}
	wide := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movq -8\(%r[a-z0-9]+\), %r[a-z0-9]+\n\s+cmpq \$1`),
		"arm64-linux":  regexp.MustCompile(`ldur x[0-9]+, \[x[0-9]+, #-8\]\n\s+cmp x[0-9]+, #1`),
	}
	narrow := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`movl -8\(%rdi\), %ecx\n\s+cmpl \$1, %ecx`),
		"arm64-linux":  regexp.MustCompile(`ldur w3, \[x0, #-8\]\n\s+cmp w3, #1`),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		cmd := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", out, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		head := arrPushHead(t, string(asm), "__fern_arr_push")
		if !narrow[target].MatchString(head) {
			t.Errorf("%s: __fern_arr_push does not read the count as a 32-bit word:\n%s", target, head)
		}
		if m := wide[target].FindString(string(asm)); m != "" {
			t.Errorf("%s: a push compares the count as a 64-bit load:\n%s", target, m)
		}
	}
}
