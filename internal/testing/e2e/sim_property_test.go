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
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

`

func genSimProgram(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString(simImports)
	b.WriteString("function main(): i32 {\n")
	fmt.Fprintf(&b, "    let d: sim.Sim = sim.new(%d as i64);\n", 1+r.Int63n(1<<30))
	b.WriteString("    let n: sim.Net = sim.net(d);\n")

	bodies := []string{"alpha", "x", "abcdefghij", "the-quick-brown-fox", "payload-0123456789"}
	nEp := 1 + r.Intn(3)
	for i := 0; i < nEp; i++ {
		host, path := i+1, fmt.Sprintf("/e%d", i)
		body := pick(r, bodies)
		if r.Intn(2) == 0 {
			fmt.Fprintf(&b, "    n = n.serve(%d, 80, %q, %q, time.duration_nanos(%d as i64));\n",
				host, path, body, pick(r, []int{1, 5, 10, 25, 40})*1000000)
		} else {
			fmt.Fprintf(&b, "    n = n.serve_chunked(%d, 80, %q, %q, time.duration_nanos(%d as i64), time.duration_nanos(%d as i64), sim.chunks_of(%d, %d));\n",
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
		fmt.Fprintf(&b, "    let fs%d: async.Future[string][] = [\n", s)
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
			case 4:
				fmt.Fprintf(&b, "        n.fetch_future(9, 80, \"/nope\")%s\n", sep)
			default:
				ep := r.Intn(nEp)
				fmt.Fprintf(&b, "        n.fetch_future(%d, 80, \"/e%d\")%s\n", ep+1, ep, sep)
			}
		}
		b.WriteString("    ];\n")
		switch r.Intn(3) {
		case 0:
			fmt.Fprintf(&b, "    let g%d: string[] = async.gather_on(d, fs%d, \"!\");\n", s, s)
			fmt.Fprintf(&b, "    print(\"g%d.len=\" + g%d.len().to_string());\n", s, s)
			fmt.Fprintf(&b, "    let i%d: i32 = 0;\n", s)
			fmt.Fprintf(&b, "    while (i%d < g%d.len()) {\n", s, s)
			fmt.Fprintf(&b, "        print(\"g%d[\" + i%d.to_string() + \"]=\" + g%d[i%d]);\n", s, s, s, s)
			fmt.Fprintf(&b, "        i%d = i%d + 1;\n    }\n", s, s)
		case 1:
			fmt.Fprintf(&b, "    let (w%d, v%d) = async.race_on(d, fs%d, \"!\");\n", s, s, s)
			fmt.Fprintf(&b, "    print(\"r%d=\" + w%d.to_string() + \":\" + v%d);\n", s, s, s)
		default:
			fmt.Fprintf(&b, "    let o%d: Option[string][] = async.with_deadline_on(d, time.duration_millis(%d), fs%d);\n",
				s, pick(r, []int{5, 15, 30, 60}), s)
			fmt.Fprintf(&b, "    let j%d: i32 = 0;\n", s)
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

const simRegressionStallPartialDeadline = `import "std/async";
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    let d: sim.Sim = sim.new(538118593 as i64);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/e0", "payload-0123456789", time.duration_nanos(10000000 as i64));
    n = n.fault_stall(1, 80, "/e0");
    n = n.serve(2, 80, "/e1", "x", time.duration_nanos(25000000 as i64));
    n = n.fault_partial(2, 80, "/e1", 0);
    n = n.fault_flaky(2, 80, "/e1", 75);
    let fs0: async.Future[string][] = [
        n.fetch_future(9, 80, "/nope"),
        sim.future_chain(d, 8000000 as i64, 6000000 as i64, 2, "c0_1")
    ];
    let o0: Option[string][] = async.with_deadline_on(d, time.duration_millis(60), fs0);
    let j0: i32 = 0;
    while (j0 < o0.len()) {
        match (o0[j0]) {
            Some(v) => { print("d0[" + j0.to_string() + "]=Some:" + v); },
            None => { print("d0[" + j0.to_string() + "]=None"); },
        }
        j0 = j0 + 1;
    }
    let fs1: async.Future[string][] = [
        n.fetch_future(2, 80, "/e1"),
        n.fetch_future(2, 80, "/e1"),
        n.fetch_future(1, 80, "/e0")
    ];
    let (w1, v1) = async.race_on(d, fs1, "!");
    print("r1=" + w1.to_string() + ":" + v1);
    let fs2: async.Future[string][] = [
        sim.future_at(d, 3000000 as i64, "a2_0"),
        sim.future_at(d, 58000000 as i64, "a2_1"),
        n.fetch_future(2, 80, "/e1")
    ];
    let o2: Option[string][] = async.with_deadline_on(d, time.duration_millis(30), fs2);
    let j2: i32 = 0;
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
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    let d: sim.Sim = sim.new(88997876 as i64);
    let n: sim.Net = sim.net(d);
    n = n.serve_chunked(1, 80, "/e0", "the-quick-brown-fox", time.duration_nanos(1000000 as i64), time.duration_nanos(5000000 as i64), sim.chunks_of(19, 3));
    n = n.fault_stall(1, 80, "/e0");
    n = n.fault_flaky(1, 80, "/e0", 75);
    let fs0: async.Future[string][] = [
        sim.future_chain(d, 32000000 as i64, 1000000 as i64, 0, "c0_0"),
        n.fetch_future(1, 80, "/e0"),
        n.fetch_future(1, 80, "/e0"),
        sim.future_chain(d, 4000000 as i64, 8000000 as i64, 1, "c0_3")
    ];
    let g0: string[] = async.gather_on(d, fs0, "!");
    print("g0.len=" + g0.len().to_string());
    let i0: i32 = 0;
    while (i0 < g0.len()) {
        print("g0[" + i0.to_string() + "]=" + g0[i0]);
        i0 = i0 + 1;
    }
    let fs1: async.Future[string][] = [
        sim.future_at(d, 14000000 as i64, "a1_0"),
        n.fetch_future(1, 80, "/e0"),
        n.fetch_future(1, 80, "/e0")
    ];
    let (w1, v1) = async.race_on(d, fs1, "!");
    print("r1=" + w1.to_string() + ":" + v1);
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    return 0;
}
`

const simRegressionFlakyStallThreeStage = `import "std/async";
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    let d: sim.Sim = sim.new(338788670 as i64);
    let n: sim.Net = sim.net(d);
    n = n.serve_chunked(1, 80, "/e0", "the-quick-brown-fox", time.duration_nanos(1000000 as i64), time.duration_nanos(2000000 as i64), sim.chunks_of(19, 4));
    n = n.fault_stall(1, 80, "/e0");
    n = n.fault_flaky(1, 80, "/e0", 75);
    let fs0: async.Future[string][] = [
        n.fetch_future(1, 80, "/e0"),
        sim.future_at(d, 60000000 as i64, "a0_1")
    ];
    let g0: string[] = async.gather_on(d, fs0, "!");
    print("g0.len=" + g0.len().to_string());
    let i0: i32 = 0;
    while (i0 < g0.len()) {
        print("g0[" + i0.to_string() + "]=" + g0[i0]);
        i0 = i0 + 1;
    }
    let fs1: async.Future[string][] = [
        sim.future_at(d, 21000000 as i64, "a1_0"),
        n.fetch_future(1, 80, "/e0")
    ];
    let o1: Option[string][] = async.with_deadline_on(d, time.duration_millis(5), fs1);
    let j1: i32 = 0;
    while (j1 < o1.len()) {
        match (o1[j1]) {
            Some(v) => { print("d1[" + j1.to_string() + "]=Some:" + v); },
            None => { print("d1[" + j1.to_string() + "]=None"); },
        }
        j1 = j1 + 1;
    }
    let fs2: async.Future[string][] = [
        n.fetch_future(1, 80, "/e0"),
        n.fetch_future(1, 80, "/e0"),
        sim.future_chain(d, 24000000 as i64, 7000000 as i64, 2, "c2_2"),
        n.fetch_future(1, 80, "/e0")
    ];
    let (w2, v2) = async.race_on(d, fs2, "!");
    print("r2=" + w2.to_string() + ":" + v2);
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    return 0;
}
`

const simRegressionRaceFailWins = `import "std/async";
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    let d: sim.Sim = sim.new(477987043 as i64);
    let n: sim.Net = sim.net(d);
    n = n.serve_chunked(1, 80, "/e0", "the-quick-brown-fox", time.duration_nanos(2000000 as i64), time.duration_nanos(2000000 as i64), sim.chunks_of(19, 5));
    n = n.serve(2, 80, "/e1", "x", time.duration_nanos(10000000 as i64));
    n = n.fault_fail(2, 80, "/e1");
    let fs0: async.Future[string][] = [
        sim.future_at(d, 47000000 as i64, "a0_0"),
        sim.future_at(d, 9000000 as i64, "a0_1"),
        n.fetch_future(2, 80, "/e1")
    ];
    let (w0, v0) = async.race_on(d, fs0, "!");
    print("r0=" + w0.to_string() + ":" + v0);
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    print("hits1=" + n.hits(2, 80, "/e1").to_string());
    return 0;
}
`

const simRegressionGatherResuspendClosure = `import "std/async";
import "std/time";
import "std/sim";
import "std/i32";
import "std/i64";

function main(): i32 {
    let d: sim.Sim = sim.new(193102384 as i64);
    let n: sim.Net = sim.net(d);
    n = n.serve(1, 80, "/e0", "abcdefghij", time.duration_nanos(40000000 as i64));
    n = n.fault_stall(1, 80, "/e0");
    n = n.fault_flaky(1, 80, "/e0", 25);
    let fs0: async.Future[string][] = [
        sim.future_chain(d, 26000000 as i64, 1000000 as i64, 0, "c0_0"),
        sim.future_chain(d, 36000000 as i64, 1000000 as i64, 1, "c0_1"),
        n.fetch_future(1, 80, "/e0"),
        sim.future_chain(d, 14000000 as i64, 3000000 as i64, 2, "c0_3")
    ];
    let g0: string[] = async.gather_on(d, fs0, "!");
    print("g0.len=" + g0.len().to_string());
    let i0: i32 = 0;
    while (i0 < g0.len()) {
        print("g0[" + i0.to_string() + "]=" + g0[i0]);
        i0 = i0 + 1;
    }
    print("now=" + d.now_ns().to_string());
    print("rng=" + d.rng_state().to_string());
    print("hits0=" + n.hits(1, 80, "/e0").to_string());
    return 0;
}`
