// Property-based differential testing for the deterministic
// simulation platform (docs/DST-PLATFORM-BRIEF.md, slice 4 — #5360).
// A generator emits small random-but-valid Fern programs over the
// whole sim surface — Sim + SimNet endpoints (scripted latencies,
// chunk schedules, the four fault modes) driven through gather_on /
// race_on / with_deadline_on over mixes of future_at / future_chain /
// fetch_future — and prints a digest of everything observable
// (results, winner indices, timed-out slots, final now_ns, rng_state,
// per-endpoint hits). The property: a sim program is a pure function
// of (program, seed), so interp, native x86-64, and wasm must produce
// BYTE-IDENTICAL stdout. Any divergence is a real bug — a miscompile
// or a nondeterminism leak — and the failing subtest prints the whole
// program for replay.
//
// This extends the numeric_property_test.go pattern to concurrency.
// Like that harness (and unlike internal/fernsmith, which generates
// scalar control-flow programs with no stdlib imports and no notion
// of futures), the generator is a dedicated, small, in-package one:
// the value here is dense coverage of one API surface, not general
// grammar coverage.
//
// TERMINATION RULE: every generated program terminates, structurally.
// (a) Each generated future has finitely many Pending steps
// (future_chain resuspends <= 3, a chunked fetch has one step per
// chunk), each on a token that either fires at a finite virtual time
// or is negative (never ready — the stall / partial faults).
// (b) The sim driver's poll_ready returns -1 once only never-ready
// tokens remain and no timeout bounds the wait, and all three
// combinators treat -1 as "stop driving": gather_on breaks and fills
// on_incomplete ("!"), race_on returns (-1, "!"), with_deadline_on
// breaks to its None fill. So even a stalled future in a plain
// gather_on cannot hang the SIM (unlike the real driver, where
// poll(2) would block forever on the same shape — which is why the
// "!" slot is part of the digest, not an error). Fetches only target
// endpoints scripted in the same program, so no future depends on
// anything outside the (program, seed) pair.
package e2e

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const simImports = `import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";

`

// genSimProgram builds one random sim program: a seeded Sim, 1-3
// scripted endpoints (random latency or chunk schedule, ~half with a
// fault mode, ~half of those wrapped flaky), then 1-3 combinator
// stages over 2-4 futures each, then the tail digest.
func genSimProgram(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString(simImports)
	b.WriteString("function main(): i32 {\n")
	fmt.Fprintf(&b, "    var d: sim.Sim = sim.new(%d as i64);\n", 1+r.Int63n(1<<30))
	b.WriteString("    var n: sim.Net = sim.net(d);\n")

	bodies := []string{"alpha", "x", "abcdefghij", "the-quick-brown-fox", "payload-0123456789"}
	nEp := 1 + r.Intn(3)
	for i := 0; i < nEp; i++ {
		host, path := i+1, fmt.Sprintf("/e%d", i)
		body := pick(r, bodies)
		if r.Intn(2) == 0 {
			fmt.Fprintf(&b, "    n = n.serve(%d, 80, %q, %q, %d as i64);\n",
				host, path, body, pick(r, []int{1, 5, 10, 25, 40})*1000000)
		} else {
			fmt.Fprintf(&b, "    n = n.serve_chunked(%d, 80, %q, %q, %d as i64, %d as i64, sim.chunks_of(%d, %d));\n",
				host, path, body,
				pick(r, []int{1, 2, 5, 10})*1000000, pick(r, []int{1, 2, 5})*1000000,
				len(body), 1+r.Intn(5))
		}
		if r.Intn(2) == 0 {
			switch r.Intn(3) {
			case 0:
				fmt.Fprintf(&b, "    n = n.fault_fail(%d, 80, %q);\n", host, path)
			case 1:
				fmt.Fprintf(&b, "    n = n.fault_stall(%d, 80, %q);\n", host, path)
			default:
				fmt.Fprintf(&b, "    n = n.fault_partial(%d, 80, %q, %d);\n", host, path, r.Intn(4))
			}
			if r.Intn(2) == 0 {
				fmt.Fprintf(&b, "    n = n.fault_flaky(%d, 80, %q, %d);\n",
					host, path, pick(r, []int{0, 25, 50, 75, 100}))
			}
		}
	}

	nStages := 1 + r.Intn(3)
	for s := 0; s < nStages; s++ {
		nf := 2 + r.Intn(3)
		fmt.Fprintf(&b, "    var fs%d: async.Future[string][] = [\n", s)
		for k := 0; k < nf; k++ {
			sep := ","
			if k == nf-1 {
				sep = ""
			}
			switch r.Intn(8) {
			case 0, 1:
				fmt.Fprintf(&b, "        sim.future_at(d, %d as i64, \"a%d_%d\")%s\n",
					(1+r.Intn(60))*1000000, s, k, sep)
			case 2, 3:
				fmt.Fprintf(&b, "        sim.future_chain(d, %d as i64, %d as i64, %d, \"c%d_%d\")%s\n",
					(1+r.Intn(40))*1000000, (1+r.Intn(8))*1000000, r.Intn(4), s, k, sep)
			case 4: // an unregistered endpoint: the dead upstream, resolves Ready("")
				fmt.Fprintf(&b, "        n.fetch_future(9, 80, \"/nope\")%s\n", sep)
			default:
				ep := r.Intn(nEp)
				fmt.Fprintf(&b, "        n.fetch_future(%d, 80, \"/e%d\")%s\n", ep+1, ep, sep)
			}
		}
		b.WriteString("    ];\n")
		switch r.Intn(3) {
		case 0:
			fmt.Fprintf(&b, "    var g%d: string[] = async.gather_on(d, fs%d, \"!\");\n", s, s)
			fmt.Fprintf(&b, "    print(\"g%d.len=\" + g%d.len().to_string());\n", s, s)
			fmt.Fprintf(&b, "    var i%d: i32 = 0;\n", s)
			fmt.Fprintf(&b, "    while (i%d < g%d.len()) {\n", s, s)
			fmt.Fprintf(&b, "        print(\"g%d[\" + i%d.to_string() + \"]=\" + g%d[i%d]);\n", s, s, s, s)
			fmt.Fprintf(&b, "        i%d = i%d + 1;\n    }\n", s, s)
		case 1:
			fmt.Fprintf(&b, "    var (w%d, v%d) = async.race_on(d, fs%d, \"!\");\n", s, s, s)
			fmt.Fprintf(&b, "    print(\"r%d=\" + w%d.to_string() + \":\" + v%d);\n", s, s, s)
		default:
			fmt.Fprintf(&b, "    var o%d: Option[string][] = async.with_deadline_on(d, %d, fs%d);\n",
				s, pick(r, []int{5, 15, 30, 60}), s)
			fmt.Fprintf(&b, "    var j%d: i32 = 0;\n", s)
			fmt.Fprintf(&b, "    while (j%d < o%d.len()) {\n", s, s)
			fmt.Fprintf(&b, "        match (o%d[j%d]) {\n", s, s)
			fmt.Fprintf(&b, "            Some(v) => { print(\"d%d[\" + j%d.to_string() + \"]=Some:\" + v); },\n", s, s)
			fmt.Fprintf(&b, "            None => { print(\"d%d[\" + j%d.to_string() + \"]=None\"); },\n", s, s)
			fmt.Fprintf(&b, "        }\n")
			fmt.Fprintf(&b, "        j%d = j%d + 1;\n    }\n", s, s)
		}
	}

	b.WriteString("    print(\"now=\" + d.now_ns().to_string());\n")
	b.WriteString("    print(\"rng=\" + d.rng_state().to_string());\n")
	for i := 0; i < nEp; i++ {
		fmt.Fprintf(&b, "    print(\"hits%d=\" + n.hits(%d, 80, \"/e%d\").to_string());\n", i, i+1, i)
	}
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

// simInterpRun runs src through `fern -interp` (the same CLI oracle
// the slice 1-3 sim gates use — the in-process interp harness in
// numeric_property_test.go doesn't register impl blocks, and the sim
// is built on a trait impl) and returns stdout + exit code.
func simInterpRun(t *testing.T, src string) (string, int) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", srcPath)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("interp exit = %d, want 0\nstderr: %s\nsrc:\n%s", code, errb.String(), src)
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

// assertSimProgramAgrees runs one sim program through interp (the
// oracle), native x86-64, and wasm, and requires byte-identical
// stdout (modulo the trailing newline) and exit 0 everywhere. Every
// failure message carries the full program source — that plus the
// seed in the subtest name is the replay workflow.
func assertSimProgramAgrees(t *testing.T, src string) {
	t.Helper()
	out, _ := simInterpRun(t, src)
	want := trimOut(out)
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		if code != 0 {
			t.Fatalf("x86_64 exit = %d, want 0\nstdout: %s\nsrc:\n%s", code, got, src)
		}
		if trimOut(got) != want {
			t.Errorf("x86_64 = %q, interp = %q\nsrc:\n%s", trimOut(got), want, src)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		comp := buildNumComponent(t, src)
		got, stderr, ec := runComponent(t, comp, runOpts{})
		if ec != 0 {
			t.Fatalf("wasmtime exit = %d\nstdout: %s\nstderr: %s\nsrc:\n%s", ec, got, stderr, src)
		}
		if trimOut(got) != want {
			t.Errorf("wasm = %q, interp = %q\nsrc:\n%s", trimOut(got), want, src)
		}
	})
}

// TestSimProperty is the deterministic seeded sweep — bounded for CI,
// with FuzzSimProperty as the deeper search entry point.
func TestSimProperty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping property sweep in -short mode")
	}
	const seeds = 25
	for s := 0; s < seeds; s++ {
		r := rand.New(rand.NewSource(int64(s)))
		src := genSimProgram(r)
		t.Run(fmt.Sprintf("seed%d", s), func(t *testing.T) {
			assertSimProgramAgrees(t, src)
		})
	}
}

// TestSimProperty_Regressions pins generator outputs verbatim (the
// numeric_property pattern), so these exact programs stay covered
// deterministically no matter how the generator evolves. Each is a
// generator seed chosen for the surface it composes; none has
// diverged across backends to date.
func TestSimProperty_Regressions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	cases := []struct{ name, src string }{
		// Stall + partial(0) + flaky(75) endpoints, a dead upstream,
		// two with_deadline stages (Some/None mix at an exact virtual
		// deadline) and a race over an all-faulted set that returns
		// the no-progress (-1, "!") after dropping every token.
		{"stall_partial_deadline_race_noprogress", simRegressionStallPartialDeadline},
		// Flaky(75)-stall chunked endpoint fetched twice inside one
		// gather: one fetch draws past the fault and drains its chunk
		// schedule, the other stalls and lands the "!" on_incomplete
		// fill (sim poll_ready -1 once only never-ready tokens remain).
		{"flaky_chunked_gather_sentinel", simRegressionFlakyChunkedGather},
		// Three stages over one flaky-stall endpoint: gather "!"
		// fill, a 5 ms deadline None, then a race won by a re-
		// suspending future_chain — five PRNG-coupled fetch draws.
		{"flaky_stall_three_stage", simRegressionFlakyStallThreeStage},
		// race_on won immediately by a fault_fail'd endpoint's
		// Ready("") — no poll at all, so now_ns stays 0 and rng_state
		// is still the raw seed.
		{"race_failed_upstream_wins_at_t0", simRegressionRaceFailWins},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertSimProgramAgrees(t, c.src)
		})
	}
}

// FuzzSimProperty drives the same generator from fuzz-provided
// entropy. Run with:
//
//	go test -run=^$ -fuzz=FuzzSimProperty ./internal/e2e
func FuzzSimProperty(f *testing.F) {
	for s := int64(0); s < 8; s++ {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))
		assertSimProgramAgrees(t, genSimProgram(r))
	})
}

// The pinned programs below are verbatim genSimProgram outputs
// (sweep seeds 5, 7, 22, and 4 of the generator as first landed),
// frozen so they replay identically no matter how the generator
// evolves.

const simRegressionStallPartialDeadline = `import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    var d: sim.Sim = sim.new(538118593 as i64);
    var n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/e0", "payload-0123456789", 10000000 as i64);
    n = n.fault_stall(1, 80, "/e0");
    n = n.serve(2, 80, "/e1", "x", 25000000 as i64);
    n = n.fault_partial(2, 80, "/e1", 0);
    n = n.fault_flaky(2, 80, "/e1", 75);
    var fs0: async.Future[string][] = [
        n.fetch_future(9, 80, "/nope"),
        sim.future_chain(d, 8000000 as i64, 6000000 as i64, 2, "c0_1")
    ];
    var o0: Option[string][] = async.with_deadline_on(d, 60, fs0);
    var j0: i32 = 0;
    while (j0 < o0.len()) {
        match (o0[j0]) {
            Some(v) => { print("d0[" + j0.to_string() + "]=Some:" + v); },
            None => { print("d0[" + j0.to_string() + "]=None"); },
        }
        j0 = j0 + 1;
    }
    var fs1: async.Future[string][] = [
        n.fetch_future(2, 80, "/e1"),
        n.fetch_future(2, 80, "/e1"),
        n.fetch_future(1, 80, "/e0")
    ];
    var (w1, v1) = async.race_on(d, fs1, "!");
    print("r1=" + w1.to_string() + ":" + v1);
    var fs2: async.Future[string][] = [
        sim.future_at(d, 3000000 as i64, "a2_0"),
        sim.future_at(d, 58000000 as i64, "a2_1"),
        n.fetch_future(2, 80, "/e1")
    ];
    var o2: Option[string][] = async.with_deadline_on(d, 30, fs2);
    var j2: i32 = 0;
    while (j2 < o2.len()) {
        match (o2[j2]) {
            Some(v) => { print("d2[" + j2.to_string() + "]=Some:" + v); },
            None => { print("d2[" + j2.to_string() + "]=None"); },
        }
        j2 = j2 + 1;
    }
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    print("hits1=" + n.hits(2, 80, "/e1").to_string());
    return 0;
}
`

const simRegressionFlakyChunkedGather = `import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    var d: sim.Sim = sim.new(88997876 as i64);
    var n: sim.Net = sim.net(d);
    n = n.serve_chunked(1, 80, "/e0", "the-quick-brown-fox", 1000000 as i64, 5000000 as i64, sim.chunks_of(19, 3));
    n = n.fault_stall(1, 80, "/e0");
    n = n.fault_flaky(1, 80, "/e0", 75);
    var fs0: async.Future[string][] = [
        sim.future_chain(d, 32000000 as i64, 1000000 as i64, 0, "c0_0"),
        n.fetch_future(1, 80, "/e0"),
        n.fetch_future(1, 80, "/e0"),
        sim.future_chain(d, 4000000 as i64, 8000000 as i64, 1, "c0_3")
    ];
    var g0: string[] = async.gather_on(d, fs0, "!");
    print("g0.len=" + g0.len().to_string());
    var i0: i32 = 0;
    while (i0 < g0.len()) {
        print("g0[" + i0.to_string() + "]=" + g0[i0]);
        i0 = i0 + 1;
    }
    var fs1: async.Future[string][] = [
        sim.future_at(d, 14000000 as i64, "a1_0"),
        n.fetch_future(1, 80, "/e0"),
        n.fetch_future(1, 80, "/e0")
    ];
    var (w1, v1) = async.race_on(d, fs1, "!");
    print("r1=" + w1.to_string() + ":" + v1);
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    return 0;
}
`

const simRegressionFlakyStallThreeStage = `import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    var d: sim.Sim = sim.new(338788670 as i64);
    var n: sim.Net = sim.net(d);
    n = n.serve_chunked(1, 80, "/e0", "the-quick-brown-fox", 1000000 as i64, 2000000 as i64, sim.chunks_of(19, 4));
    n = n.fault_stall(1, 80, "/e0");
    n = n.fault_flaky(1, 80, "/e0", 75);
    var fs0: async.Future[string][] = [
        n.fetch_future(1, 80, "/e0"),
        sim.future_at(d, 60000000 as i64, "a0_1")
    ];
    var g0: string[] = async.gather_on(d, fs0, "!");
    print("g0.len=" + g0.len().to_string());
    var i0: i32 = 0;
    while (i0 < g0.len()) {
        print("g0[" + i0.to_string() + "]=" + g0[i0]);
        i0 = i0 + 1;
    }
    var fs1: async.Future[string][] = [
        sim.future_at(d, 21000000 as i64, "a1_0"),
        n.fetch_future(1, 80, "/e0")
    ];
    var o1: Option[string][] = async.with_deadline_on(d, 5, fs1);
    var j1: i32 = 0;
    while (j1 < o1.len()) {
        match (o1[j1]) {
            Some(v) => { print("d1[" + j1.to_string() + "]=Some:" + v); },
            None => { print("d1[" + j1.to_string() + "]=None"); },
        }
        j1 = j1 + 1;
    }
    var fs2: async.Future[string][] = [
        n.fetch_future(1, 80, "/e0"),
        n.fetch_future(1, 80, "/e0"),
        sim.future_chain(d, 24000000 as i64, 7000000 as i64, 2, "c2_2"),
        n.fetch_future(1, 80, "/e0")
    ];
    var (w2, v2) = async.race_on(d, fs2, "!");
    print("r2=" + w2.to_string() + ":" + v2);
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    return 0;
}
`

const simRegressionRaceFailWins = `import "std/async";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    var d: sim.Sim = sim.new(477987043 as i64);
    var n: sim.Net = sim.net(d);
    n = n.serve_chunked(1, 80, "/e0", "the-quick-brown-fox", 2000000 as i64, 2000000 as i64, sim.chunks_of(19, 5));
    n = n.serve(2, 80, "/e1", "x", 10000000 as i64);
    n = n.fault_fail(2, 80, "/e1");
    var fs0: async.Future[string][] = [
        sim.future_at(d, 47000000 as i64, "a0_0"),
        sim.future_at(d, 9000000 as i64, "a0_1"),
        n.fetch_future(2, 80, "/e1")
    ];
    var (w0, v0) = async.race_on(d, fs0, "!");
    print("r0=" + w0.to_string() + ":" + v0);
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    print("hits1=" + n.hits(2, 80, "/e1").to_string());
    return 0;
}
`
