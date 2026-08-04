package e2eselfhost

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Nothing compares how MUCH the two compilers allocate. That is how they
// developed opposite cliffs undetected — a shape that leaks megabytes under
// one and nothing under the other stays green everywhere, because every
// existing gate asks "is the answer right?" and this class of bug never
// changes the answer. docs/TEST-GATES.md lists allocation volume under "what
// nothing gates at all"; this is that gate.
//
// It compares two probes, both now supported by both compilers:
//
//   - __heap_bump_bytes()        bytes handed out fresh (what the freelist
//                                could not recycle)
//   - __arr_push_shared_count()  appends that copied a buffer which still had
//                                spare capacity — the rc==1 cliff
//
// WHAT IT DOES NOT ASSERT: byte equality. The two runtimes lay their boxes out
// differently (header sizes, capacity schedules, string representation), so
// identical allocation behaviour legitimately produces different totals. An
// exact-match gate here would be noise, and noise gets muted. Two comparisons
// survive that objection:
//
//  1. the cliff counter agreeing on ZERO vs NON-ZERO. It counts events, not
//     bytes, so it is layout-free. Not exact equality either — the two
//     runtimes grow capacity on different schedules, so the same program can
//     legitimately cross the cliff a different NUMBER of times. Whether it
//     crosses at all is the part that means something.
//  2. the per-churn bump delta staying within a RATIO. The divergences this
//     exists to catch were three and four orders of magnitude; a ratio bound
//     catches those while tolerating layout.
//
// Divergences are LISTED, not skipped — see the testdata file, and
// selfhost-wasm-known-divergences.txt for the convention. A new divergence
// fails, and a listed shape that comes back WITHIN bound also fails, because
// an allowlist nobody prunes is where bugs go to be forgotten.
//
// x86-64 only. The comparison is between COMPILERS, not between targets, so
// running the same pair again under qemu-aarch64 costs minutes to re-answer a
// question the x86-64 pair already answered.

// allocDiffCase is one shape, measured twice per compiler: once for bump
// growth, once for the cliff counter.
//
// decls must define `churn(n: i32): i32` — one self-contained unit of work
// whose allocations are all dead when it returns. Both metric programs call it
// the same way, so the bump delta between two identical churns is exactly what
// the first churn failed to give back.
type allocDiffCase struct {
	name  string
	decls string
	n     int
	// maxRatio bounds max(native, selfhost) / max(min(native, selfhost), 1)
	// on per-churn KB. 1 in the denominator so a shape that reclaims fully on
	// one side (0 KB) does not produce a division by zero — it makes a 0-vs-K
	// split read as ratio K, which is the right severity for small K and
	// correctly damning for large K.
	maxRatio int
}

var allocDiffCases = []allocDiffCase{
	{
		// The shape every byte-emitter in the self-host compiler is built
		// from: an accumulator threaded through a borrowed param and handed
		// back. Nothing else holds the buffer.
		name: "append-threaded-through-call",
		decls: `function step(acc: i32[], v: i32): i32[] { return acc.append(v); }
function churn(n: i32): i32 {
    var a: i32[] = [];
    var i: i32 = 0;
    while (i < n) { a = step(a, i); i = i + 1; }
    return a.len();
}`,
		n:        400,
		maxRatio: 8,
	},
	{
		// `.with` through a borrowed param — a functional element update.
		// This is the shape docs/TEST-GATES.md cites as having gone 4688 MB
		// native / 0 MB self-host.
		name: "with-through-borrowed-param",
		decls: `function fill(buf: i32[], n: i32): i32[] {
    var i: i32 = 0;
    while (i < n) { buf = buf.with(i, i * 2); i = i + 1; }
    return buf;
}
function churn(n: i32): i32 {
    var b: i32[] = [];
    var i: i32 = 0;
    while (i < n) { b = b.append(0); i = i + 1; }
    b = fill(b, n);
    return b[n - 1];
}`,
		// n is small because this shape's native leak is QUADRATIC — each
		// `.with` abandons an n-element buffer, n times — so the per-churn
		// figure has to stay inside the byte the exit code can carry. At
		// n=200 it overflowed and the guard reported 252.
		n:        80,
		maxRatio: 8,
	},
	{
		// A fresh array per iteration, dead at the end of the body. The
		// baseline both compilers should reclaim completely; if this one ever
		// diverges, the freelist itself has regressed on one side.
		name: "fresh-array-per-iteration",
		decls: `function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var row: i32[] = [i, i + 1, i + 2];
        s = (s + row[0]) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        400,
		maxRatio: 8,
	},
	{
		// A struct carrying an array field, rebuilt each iteration — the
		// container shape, where a missed deep drop shows up on one side as
		// steady growth.
		name: "struct-with-array-field",
		decls: `struct Row { xs: i32[], k: i32 }
function mk(k: i32): Row { return Row { xs: [k, k + 1, k + 2], k: k }; }
function churn(n: i32): i32 {
    var i: i32 = 0;
    var s: i32 = 0;
    while (i < n) {
        var r: Row = mk(i);
        s = (s + r.xs[1] + r.k) % 251;
        i = i + 1;
    }
    return s;
}`,
		n:        400,
		maxRatio: 8,
	},
}

// bumpSrc returns a program whose EXIT CODE is the per-churn bump growth in
// KB. Two identical churns, measured between: whatever the first failed to
// give back is what the second has to allocate fresh.
//
// The metric rides the exit code rather than stdout because the self-host
// driver resolves no stdlib, so `.to_string()` is unavailable to it. That caps
// the readable range at one byte, hence the 240 KB guard — a shape that leaks
// past it reports 252 rather than silently wrapping into a plausible number.
func (c allocDiffCase) bumpSrc() string {
	return fmt.Sprintf(`%s
function main(): i32 {
    var w: i32 = churn(%d);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(%d);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (w != x) { return 251; }
    var kb: i32 = (b2 - b1) / 1024;
    if (kb > 240) { return 252; }
    return kb;
}
`, c.decls, c.n, c.n)
}

// cliffSrc returns a program whose exit code is the rc==1 append cliff count.
func (c allocDiffCase) cliffSrc() string {
	return fmt.Sprintf(`%s
function main(): i32 {
    var w: i32 = churn(%d);
    if (w < 0) { return 251; }
    var n: i32 = __arr_push_shared_count();
    if (n > 240) { return 240; }
    return n;
}
`, c.decls, c.n)
}

// knownAllocDivergence is one entry of the testdata allowlist.
type knownAllocDivergence struct {
	name     string
	native   int
	selfhost int
}

// loadKnownAllocDivergences parses the testdata allowlist. Format per line:
//
//	<case-name> <native-kb> <selfhost-kb> <reason...>
//
// `#` comments and blank lines are ignored. The recorded KB figures are what
// was measured when the entry was written; they are reported alongside the
// live numbers on failure so a drift is visible without digging through git.
func loadKnownAllocDivergences(t *testing.T) map[string]knownAllocDivergence {
	t.Helper()
	path := filepath.Join("testdata", "alloc-differential-known-divergences.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string]knownAllocDivergence{}
	sc := bufio.NewScanner(f)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("%s:%d: want `<name> <native-kb> <selfhost-kb> <reason>`, got %q", path, ln, line)
		}
		nat, err1 := strconv.Atoi(fields[1])
		sh, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			t.Fatalf("%s:%d: non-numeric KB figures in %q", path, ln, line)
		}
		out[fields[0]] = knownAllocDivergence{name: fields[0], native: nat, selfhost: sh}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

// allocRatio is the severity measure: how many times more one compiler
// allocated than the other, in either direction.
func allocRatio(native, selfhost int) int {
	hi, lo := native, selfhost
	if lo > hi {
		hi, lo = lo, hi
	}
	if lo < 1 {
		lo = 1
	}
	return hi / lo
}

// TestSelfHostAllocDifferentialX86_64 is the gate. Each shape is compiled and
// run by BOTH compilers and their allocation behaviour compared.
func TestSelfHostAllocDifferentialX86_64(t *testing.T) {
	known := loadKnownAllocDivergences(t)
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	// selfHostExit compiles src with the self-hosted x86-64 driver, links it,
	// runs it, and returns the exit code.
	selfHostExit := func(t *testing.T, label, src string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(src))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", label)
		}
		bin := buildBin(t, gcc, dir, label, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	seen := map[string]bool{}
	for _, tc := range allocDiffCases {
		seen[tc.name] = true
		t.Run(tc.name, func(t *testing.T) {
			// --- the cliff counter: zero vs non-zero must agree ---
			_, natCliff := compileAndRunX86_64(t, tc.cliffSrc())
			shCliff := selfHostExit(t, tc.name+"-cliff", tc.cliffSrc())
			if natCliff >= 250 || shCliff >= 250 {
				t.Fatalf("cliff probe self-check failed (native=%d self-host=%d); "+
					"251 means the two churns disagreed", natCliff, shCliff)
			}
			if (natCliff == 0) != (shCliff == 0) {
				t.Errorf("rc==1 append cliff disagrees: native crossed it %d time(s), "+
					"self-host %d — one compiler is copying a buffer the other mutates "+
					"in place, which is the O(n) vs O(n²) split", natCliff, shCliff)
			}

			// --- bump growth: within a ratio, or listed ---
			_, natKB := compileAndRunX86_64(t, tc.bumpSrc())
			shKB := selfHostExit(t, tc.name+"-bump", tc.bumpSrc())
			for who, got := range map[string]int{"native": natKB, "self-host": shKB} {
				if got == 251 {
					t.Fatalf("%s: the two churns returned different values — the probe "+
						"is not measuring identical work", who)
				}
				if got == 252 {
					t.Fatalf("%s: per-churn growth exceeded the 240 KB the exit code can "+
						"carry; lower this case's n", who)
				}
			}

			ratio := allocRatio(natKB, shKB)
			entry, listed := known[tc.name]
			switch {
			case listed && ratio <= tc.maxRatio:
				t.Errorf("%s is listed as a known divergence (recorded native=%d KB "+
					"self-host=%d KB) but now measures native=%d KB self-host=%d KB, "+
					"ratio %dx — within the %dx bound. If this was fixed, delete the "+
					"entry; a stale allowlist hides the next regression",
					tc.name, entry.native, entry.selfhost, natKB, shKB, ratio, tc.maxRatio)
			case listed:
				t.Logf("known divergence: native=%d KB self-host=%d KB (%dx); "+
					"recorded as native=%d KB self-host=%d KB",
					natKB, shKB, ratio, entry.native, entry.selfhost)
			case ratio > tc.maxRatio:
				t.Errorf("allocation differs %dx between compilers: native=%d KB "+
					"self-host=%d KB per churn (bound %dx). Either a real regression, "+
					"or — if intended — add it to "+
					"testdata/alloc-differential-known-divergences.txt with the reason",
					ratio, natKB, shKB, tc.maxRatio)
			}
		})
	}

	// An entry naming a shape the corpus no longer contains is dead weight
	// that reads as coverage.
	for name := range known {
		if !seen[name] {
			t.Errorf("testdata lists %q but no case by that name exists — "+
				"rename or remove the entry", name)
		}
	}
}
