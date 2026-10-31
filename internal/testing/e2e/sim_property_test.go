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

// simPrelude scripts an upstream's futures under the sim clock: drain
// resolves to body at the last scheduled chunk, drip wakes for k chunks
// and then stalls, and faulty spends one draw of the sim's PRNG.
const simPrelude = `import "std/async";
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

function at_ms(ms: i32): Option[Instant] {
    return Some(sim.at(time.duration_millis(ms as i64)));
}

function stall(): async.Future[string] {
    let none: i32[] = [];
    return Pending(async.Wait { set: none, until: None }, (wake: async.Wake): async.Future[string] => stall());
}

function drip(at: i32[], i: i32, k: i32): async.Future[string] {
    if (i >= k) {
        return stall();
    }
    let none: i32[] = [];
    return Pending(async.Wait { set: none, until: at_ms(at[i]) }, (wake: async.Wake): async.Future[string] => drip(at, i + 1, k));
}

function drained(at: i32[], body: string, i: i32): async.Future[string] {
    if (i + 1 >= at.len()) {
        return Ready(body);
    }
    return drain(at, body, i + 1);
}

function drain(at: i32[], body: string, i: i32): async.Future[string] {
    let none: i32[] = [];
    return Pending(async.Wait { set: none, until: at_ms(at[i]) }, (wake: async.Wake): async.Future[string] => drained(at, body, i));
}

function schedule(d: sim.Sim, first_ms: i32, chunk_ms: i32, chunks: i32): i32[] {
    let now: i32 = (d.elapsed().as_nanos() / 1000000) as i32;
    let at: i32[] = [];
    for c in 0..chunks {
        at = at.append(now + first_ms + c * chunk_ms);
    }
    return at;
}

function faulty(d: sim.Sim, pct: i32): boolean {
    return (d.random_i32() & 1023) % 100 < pct;
}

`

// simUpstream is one scripted upstream: fault is "" (healthy), "fail",
// "stall" or "partial"; flakyPc is the percentage of calls the fault
// takes, or -1 for every call.
type simUpstream struct {
	body              string
	firstMs, chunkMs  int
	chunks            int
	fault             string
	partialK, flakyPc int
}

func (u simUpstream) faultExpr() string {
	switch u.fault {
	case "fail":
		return `Ready("")`
	case "stall":
		return "stall()"
	default:
		k := min(max(u.partialK, 0), u.chunks)
		return fmt.Sprintf("drip(schedule(d, %d, %d, %d), 0, %d)", u.firstMs, u.chunkMs, u.chunks, k)
	}
}

func (u simUpstream) function(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "function %s(d: sim.Sim): async.Future[string] {\n", name)
	if u.fault != "" {
		if u.flakyPc >= 0 {
			fmt.Fprintf(&b, "    if (faulty(d, %d)) {\n        return %s;\n    }\n", u.flakyPc, u.faultExpr())
		} else {
			fmt.Fprintf(&b, "    return %s;\n", u.faultExpr())
		}
	}
	fmt.Fprintf(&b, "    return drain(schedule(d, %d, %d, %d), %q, 0);\n}\n\n", u.firstMs, u.chunkMs, u.chunks, u.body)
	return b.String()
}

func genSimProgram(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString(simPrelude)

	bodies := []string{"alpha", "x", "abcdefghij", "the-quick-brown-fox", "payload-0123456789"}
	nUp := 1 + r.Intn(3)
	for i := 0; i < nUp; i++ {
		u := simUpstream{body: pick(r, bodies), chunks: 1, flakyPc: -1}
		if r.Intn(2) == 0 {
			u.firstMs = pick(r, []int{1, 5, 10, 25, 40})
		} else {
			u.firstMs, u.chunkMs = pick(r, []int{1, 2, 5, 10}), pick(r, []int{1, 2, 5})
			size := 1 + r.Intn(5)
			u.chunks = (len(u.body) + size - 1) / size
		}
		if r.Intn(2) == 0 {
			u.fault = []string{"fail", "stall", "partial"}[r.Intn(3)]
			u.partialK = r.Intn(4)
			if r.Intn(2) == 0 {
				u.flakyPc = pick(r, []int{0, 25, 50, 75, 100})
			}
		}
		b.WriteString(u.function(fmt.Sprintf("up%d", i)))
	}

	b.WriteString("function main(): i32 {\n")
	fmt.Fprintf(&b, "    let d: sim.Sim = sim.new(%d as i64);\n", 1+r.Int63n(1<<30))
	nStages := 1 + r.Intn(3)
	for s := 0; s < nStages; s++ {
		nf := 2 + r.Intn(3)
		fmt.Fprintf(&b, "    let fs%d: async.Future[string][] = [\n", s)
		for k := 0; k < nf; k++ {
			sep := ","
			if k == nf-1 {
				sep = ""
			}
			switch r.Intn(8) {
			case 0, 1:
				fmt.Fprintf(&b, "        sim.future_at(sim.at(time.duration_nanos(%d)), \"a%d_%d\")%s\n",
					(1+r.Intn(60))*1000000, s, k, sep)
			case 2, 3:
				fmt.Fprintf(&b, "        sim.future_chain(sim.at(time.duration_nanos(%d)), time.duration_nanos(%d), %d, \"c%d_%d\")%s\n",
					(1+r.Intn(40))*1000000, (1+r.Intn(8))*1000000, r.Intn(4), s, k, sep)
			case 4:
				fmt.Fprintf(&b, "        Ready(\"\")%s\n", sep)
			default:
				fmt.Fprintf(&b, "        up%d(d)%s\n", r.Intn(nUp), sep)
			}
		}
		b.WriteString("    ];\n")
		switch r.Intn(3) {
		case 0:
			fmt.Fprintf(&b, "    match (sim.block_on(d, async.gather(fs%d))) {\n", s)
			fmt.Fprintf(&b, "        Some(g%d) => {\n", s)
			fmt.Fprintf(&b, "            print(\"g%d.len=\" + g%d.len().to_string());\n", s, s)
			fmt.Fprintf(&b, "            for i%d in 0..g%d.len() {\n", s, s)
			fmt.Fprintf(&b, "                print(\"g%d[\" + i%d.to_string() + \"]=\" + g%d[i%d]);\n", s, s, s, s)
			fmt.Fprintf(&b, "            }\n        },\n")
			fmt.Fprintf(&b, "        None => { print(\"g%d=None\"); }\n    }\n", s)
		case 1:
			fmt.Fprintf(&b, "    match (sim.block_on(d, async.race(fs%d))) {\n", s)
			fmt.Fprintf(&b, "        Some(w%d) => { print(\"r%d=\" + w%d.0.to_string() + \":\" + w%d.1); },\n", s, s, s, s)
			fmt.Fprintf(&b, "        None => { print(\"r%d=None\"); }\n    }\n", s)
		default:
			fmt.Fprintf(&b, "    let o%d: Option[string][] = sim.block_on(d, async.with_deadline(d.now().add(time.duration_millis(%d)), fs%d)).unwrap_or([]);\n",
				s, pick(r, []int{5, 15, 30, 60}), s)
			fmt.Fprintf(&b, "    for j%d in 0..o%d.len() {\n", s, s)
			fmt.Fprintf(&b, "        match (o%d[j%d]) {\n", s, s)
			fmt.Fprintf(&b, "            Some(v) => { print(\"d%d[\" + j%d.to_string() + \"]=Some:\" + v); },\n", s, s)
			fmt.Fprintf(&b, "            None => { print(\"d%d[\" + j%d.to_string() + \"]=None\"); },\n", s, s)
			fmt.Fprintf(&b, "        }\n    }\n")
		}
	}

	b.WriteString("    print(\"now=\" + d.elapsed().as_nanos().to_string());\n")
	b.WriteString("    print(\"rng=\" + d.rng_state().to_string());\n")
	b.WriteString("    return 0;\n}\n")
	return b.String()
}

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
	t.Run("wasm32-wasi", func(t *testing.T) {
		comp := buildCLIComponent(t, src)
		got, stderr, ec := runWasmArtifact(t, comp, runOpts{})
		if ec != 0 {
			t.Fatalf("wasmtime exit = %d\nstdout: %s\nstderr: %s\nsrc:\n%s", ec, got, stderr, src)
		}
		if trimOut(got) != want {
			t.Errorf("wasm = %q, interp = %q\nsrc:\n%s", trimOut(got), want, src)
		}
	})
}

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

func TestSimProperty_Regressions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	cases := []struct{ name, src string }{
		{"stall_partial_deadline_race_noprogress", simRegressionStallPartialDeadline},
		{"flaky_chunked_gather_sentinel", simRegressionFlakyChunkedGather},
		{"flaky_stall_three_stage", simRegressionFlakyStallThreeStage},
		{"race_failed_upstream_wins_at_t0", simRegressionRaceFailWins},
		{"gather_resuspends_enum_payload_closure", simRegressionGatherResuspendClosure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertSimProgramAgrees(t, c.src)
		})
	}
}

func FuzzSimProperty(f *testing.F) {
	for s := int64(0); s < 8; s++ {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, seed int64) {
		r := rand.New(rand.NewSource(seed))
		assertSimProgramAgrees(t, genSimProgram(r))
	})
}

const simRegressionStallPartialDeadline = simPrelude + `function up0(d: sim.Sim): async.Future[string] {
    return stall();
}

function up1(d: sim.Sim): async.Future[string] {
    if (faulty(d, 75)) {
        return drip(schedule(d, 25, 0, 1), 0, 0);
    }
    return drain(schedule(d, 25, 0, 1), "x", 0);
}

function main(): i32 {
    let d: sim.Sim = sim.new(538118593 as i64);
    let fs0: async.Future[string][] = [
        Ready(""),
        sim.future_chain(sim.at(time.duration_nanos(8000000)), time.duration_nanos(6000000), 2, "c0_1")
    ];
    let o0: Option[string][] = sim.block_on(d, async.with_deadline(d.now().add(time.duration_millis(60)), fs0)).unwrap_or([]);
    for j0 in 0..o0.len() {
        match (o0[j0]) {
            Some(v) => { print("d0[" + j0.to_string() + "]=Some:" + v); },
            None => { print("d0[" + j0.to_string() + "]=None"); },
        }
    }
    let fs1: async.Future[string][] = [
        up1(d),
        up1(d),
        up0(d)
    ];
    match (sim.block_on(d, async.race(fs1))) {
        Some(w1) => { print("r1=" + w1.0.to_string() + ":" + w1.1); },
        None => { print("r1=None"); }
    }
    let fs2: async.Future[string][] = [
        sim.future_at(sim.at(time.duration_nanos(3000000)), "a2_0"),
        sim.future_at(sim.at(time.duration_nanos(58000000)), "a2_1"),
        up1(d)
    ];
    let o2: Option[string][] = sim.block_on(d, async.with_deadline(d.now().add(time.duration_millis(30)), fs2)).unwrap_or([]);
    for j2 in 0..o2.len() {
        match (o2[j2]) {
            Some(v) => { print("d2[" + j2.to_string() + "]=Some:" + v); },
            None => { print("d2[" + j2.to_string() + "]=None"); },
        }
    }
    print("now=" + d.elapsed().as_nanos().to_string());
    print("rng=" + d.rng_state().to_string());
    return 0;
}
`

const simRegressionFlakyChunkedGather = simPrelude + `function up0(d: sim.Sim): async.Future[string] {
    if (faulty(d, 75)) {
        return stall();
    }
    return drain(schedule(d, 1, 5, 7), "the-quick-brown-fox", 0);
}

function main(): i32 {
    let d: sim.Sim = sim.new(88997876 as i64);
    let fs0: async.Future[string][] = [
        sim.future_chain(sim.at(time.duration_nanos(32000000)), time.duration_nanos(1000000), 0, "c0_0"),
        up0(d),
        up0(d),
        sim.future_chain(sim.at(time.duration_nanos(4000000)), time.duration_nanos(8000000), 1, "c0_3")
    ];
    match (sim.block_on(d, async.gather(fs0))) {
        Some(g0) => {
            print("g0.len=" + g0.len().to_string());
            for i0 in 0..g0.len() {
                print("g0[" + i0.to_string() + "]=" + g0[i0]);
            }
        },
        None => { print("g0=None"); }
    }
    let fs1: async.Future[string][] = [
        sim.future_at(sim.at(time.duration_nanos(14000000)), "a1_0"),
        up0(d),
        up0(d)
    ];
    match (sim.block_on(d, async.race(fs1))) {
        Some(w1) => { print("r1=" + w1.0.to_string() + ":" + w1.1); },
        None => { print("r1=None"); }
    }
    print("now=" + d.elapsed().as_nanos().to_string());
    print("rng=" + d.rng_state().to_string());
    return 0;
}
`

const simRegressionFlakyStallThreeStage = simPrelude + `function up0(d: sim.Sim): async.Future[string] {
    if (faulty(d, 75)) {
        return stall();
    }
    return drain(schedule(d, 1, 2, 5), "the-quick-brown-fox", 0);
}

function main(): i32 {
    let d: sim.Sim = sim.new(338788670 as i64);
    let fs0: async.Future[string][] = [
        up0(d),
        sim.future_at(sim.at(time.duration_nanos(60000000)), "a0_1")
    ];
    match (sim.block_on(d, async.gather(fs0))) {
        Some(g0) => {
            print("g0.len=" + g0.len().to_string());
            for i0 in 0..g0.len() {
                print("g0[" + i0.to_string() + "]=" + g0[i0]);
            }
        },
        None => { print("g0=None"); }
    }
    let fs1: async.Future[string][] = [
        sim.future_at(sim.at(time.duration_nanos(21000000)), "a1_0"),
        up0(d)
    ];
    let o1: Option[string][] = sim.block_on(d, async.with_deadline(d.now().add(time.duration_millis(5)), fs1)).unwrap_or([]);
    for j1 in 0..o1.len() {
        match (o1[j1]) {
            Some(v) => { print("d1[" + j1.to_string() + "]=Some:" + v); },
            None => { print("d1[" + j1.to_string() + "]=None"); },
        }
    }
    let fs2: async.Future[string][] = [
        up0(d),
        up0(d),
        sim.future_chain(sim.at(time.duration_nanos(24000000)), time.duration_nanos(7000000), 2, "c2_2"),
        up0(d)
    ];
    match (sim.block_on(d, async.race(fs2))) {
        Some(w2) => { print("r2=" + w2.0.to_string() + ":" + w2.1); },
        None => { print("r2=None"); }
    }
    print("now=" + d.elapsed().as_nanos().to_string());
    print("rng=" + d.rng_state().to_string());
    return 0;
}
`

const simRegressionRaceFailWins = simPrelude + `function main(): i32 {
    let d: sim.Sim = sim.new(477987043 as i64);
    let fs0: async.Future[string][] = [
        sim.future_at(sim.at(time.duration_nanos(47000000)), "a0_0"),
        sim.future_at(sim.at(time.duration_nanos(9000000)), "a0_1"),
        Ready("")
    ];
    match (sim.block_on(d, async.race(fs0))) {
        Some(w0) => { print("r0=" + w0.0.to_string() + ":" + w0.1); },
        None => { print("r0=None"); }
    }
    print("now=" + d.elapsed().as_nanos().to_string());
    print("rng=" + d.rng_state().to_string());
    return 0;
}
`

const simRegressionGatherResuspendClosure = simPrelude + `function up0(d: sim.Sim): async.Future[string] {
    if (faulty(d, 25)) {
        return stall();
    }
    return drain(schedule(d, 40, 0, 1), "abcdefghij", 0);
}

function main(): i32 {
    let d: sim.Sim = sim.new(193102384 as i64);
    let fs0: async.Future[string][] = [
        sim.future_chain(sim.at(time.duration_nanos(26000000)), time.duration_nanos(1000000), 0, "c0_0"),
        sim.future_chain(sim.at(time.duration_nanos(36000000)), time.duration_nanos(1000000), 1, "c0_1"),
        up0(d),
        sim.future_chain(sim.at(time.duration_nanos(14000000)), time.duration_nanos(3000000), 2, "c0_3")
    ];
    match (sim.block_on(d, async.gather(fs0))) {
        Some(g0) => {
            print("g0.len=" + g0.len().to_string());
            for i0 in 0..g0.len() {
                print("g0[" + i0.to_string() + "]=" + g0[i0]);
            }
        },
        None => { print("g0=None"); }
    }
    print("now=" + d.elapsed().as_nanos().to_string());
    print("rng=" + d.rng_state().to_string());
    return 0;
}`
