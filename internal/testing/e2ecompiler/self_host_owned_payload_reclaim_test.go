package e2ecompiler

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func readChunkDrainSrc(size, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let r: Reader = stdin();
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < %d) {
        match (r.read_chunk(%d)) {
            Ok(chunk) => { acc = acc + chunk.len(); },
            Err(e) => { return 9; }
        }
        i = i + 1;
    }
    return acc %% 251;
}`, rounds, size)
}

func readChunkDiscardSrc(size, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    let r: Reader = stdin();
    let i: i32 = 0;
    while (i < %d) {
        match (r.read_chunk(%d)) {
            Ok(_) => { i = i + 1; },
            Err(e) => { return 9; }
        }
    }
    return 0;
}`, rounds, size)
}

func runWithStdin(t *testing.T, runner []string, bin string, stdin []byte) (string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	var errBuf bytes.Buffer
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	return errBuf.String(), cmd.ProcessState.ExitCode()
}

func leakSummaryOf(t *testing.T, label, stderr string) (allocs, frees, live int64) {
	t.Helper()
	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary — FERN_LEAKCHECK did not take effect", label)
	}
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", label, summary, err)
	}
	return allocs, frees, live
}

func TestSelfHostOwnedPayloadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const rounds = 24

	for _, tc := range []struct {
		name          string
		narrow, wide  string
		inNarrow      []byte
		inWide        []byte
		wantExitEqual bool
	}{
		{
			name:     "read_chunk_bound",
			narrow:   readChunkDrainSrc(16, rounds),
			wide:     readChunkDrainSrc(1024, rounds),
			inNarrow: bytes.Repeat([]byte{'x'}, 16*rounds),
			inWide:   bytes.Repeat([]byte{'x'}, 1024*rounds),
		},
		{
			name:          "read_chunk_discarded",
			narrow:        readChunkDiscardSrc(16, rounds),
			wide:          readChunkDiscardSrc(1024, rounds),
			inNarrow:      bytes.Repeat([]byte{'x'}, 16*rounds),
			inWide:        bytes.Repeat([]byte{'x'}, 1024*rounds),
			wantExitEqual: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			measure := func(label, src string, stdin []byte) (int64, int64, int64, int) {
				asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
				bin := buildBin(t, gcc, dir, tc.name+"_"+label, asm)
				stderr, exit := runWithStdin(t, runner, bin, stdin)
				a, f, l := leakSummaryOf(t, label, stderr)
				return a, f, l, exit
			}
			na, nf, nl, nx := measure("narrow", tc.narrow, tc.inNarrow)
			wa, wf, wl, wx := measure("wide", tc.wide, tc.inWide)
			if nx == 9 || wx == 9 {
				t.Fatalf("probe hit its error arm (narrow exit %d, wide exit %d) — stdin was short", nx, wx)
			}
			if tc.wantExitEqual && nx != wx {
				t.Errorf("narrow exit %d != wide exit %d", nx, wx)
			}
			if na == 0 || wa == 0 {
				t.Fatal("program allocated nothing — the probe is not exercising the path")
			}
			if nf == 0 || wf == 0 {
				t.Fatalf("nothing was freed (narrow %d/%d, wide %d/%d) — the payload is still a borrow",
					na, nf, wa, wf)
			}
			if nl != wl {
				t.Errorf("live_bytes narrow=%d wide=%d (allocs %d/%d, frees %d/%d): the owned "+
					"payload must not scale with the chunk size — what is left is the per-call "+
					"immortal Option/Result box, a constant",
					nl, wl, na, wa, nf, wf)
			}
		})
	}
}

func TestSelfHostOwnedPayloadShortReadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const rounds = 24
	src := readChunkDrainSrc(1024, rounds)
	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	bin := buildBin(t, gcc, dir, "short_read", asm)
	stderr, exit := runWithStdin(t, runner, bin, bytes.Repeat([]byte{'x'}, 16*rounds))
	if exit == 9 {
		t.Fatal("probe hit its error arm")
	}
	shortAllocs, shortFrees, shortLive := leakSummaryOf(t, "short", stderr)

	fullSrc := readChunkDrainSrc(16, rounds)
	fullAsm := hevCompile(t, runner, driverBin, fullSrc, []string{"FERN_LEAKCHECK=1"})
	fullBin := buildBin(t, gcc, dir, "full_read", fullAsm)
	fullStderr, fullExit := runWithStdin(t, runner, fullBin, bytes.Repeat([]byte{'x'}, 16*rounds))
	if fullExit == 9 {
		t.Fatal("full-read probe hit its error arm")
	}
	fullAllocs, fullFrees, fullLive := leakSummaryOf(t, "full", fullStderr)

	if exit != fullExit {
		t.Errorf("short-read exit %d != full-read exit %d — the copy changed the bytes", exit, fullExit)
	}
	if shortFrees == 0 || fullFrees == 0 {
		t.Fatalf("nothing was freed (short %d/%d, full %d/%d)", shortAllocs, shortFrees, fullAllocs, fullFrees)
	}
	if shortLive != fullLive {
		t.Errorf("live_bytes short-read=%d full-read=%d: a short read must be copied into an "+
			"exact-size block and the oversized one given back, or the slack strands below "+
			"the freelist class the block lands in",
			shortLive, fullLive)
	}
}

func TestSelfHostOwnedPayloadHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name  string
		src   string
		stdin []byte
		want  int
	}{
		{
			name: "escapes_through_append",
			src: `function main(): i32 {
    let r: Reader = stdin();
    let held: string[] = [];
    let i: i32 = 0;
    while (i < 4) {
        match (r.read_chunk(16)) {
            Ok(chunk) => { held = held.append(chunk); },
            Err(e) => { return 9; }
        }
        i = i + 1;
    }
    return held[0].len() + held[3].len() + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 64),
			want:  32,
		},
		{
			name: "passed_to_user_fn_that_keeps_it",
			src: `function eat(n: i32): i32 {
    let s: string = "x";
    let i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function keep(t: string): string[] {
    let out: string[] = [];
    out = out.append(t);
    return out;
}
function main(): i32 {
    let r: Reader = stdin();
    let held: string[] = [];
    let i: i32 = 0;
    while (i < 4) {
        match (r.read_chunk(16)) {
            Ok(chunk) => { let one: string[] = keep(chunk); held = held.append(one[0]); },
            Err(e) => { return 9; }
        }
        i = i + 1;
    }
    let junk: i32 = eat(300);
    return held[0].len() + held[3].len() + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 64),
			want:  32,
		},
		{
			name: "returned_from_arm",
			src: `function first(r: Reader): string {
    match (r.read_chunk(16)) {
        Ok(chunk) => { return chunk; },
        Err(e) => { return ""; }
    }
    return "";
}
function main(): i32 {
    let r: Reader = stdin();
    let s: string = first(r);
    return s.len() + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 64),
			want:  16,
		},
		{
			name: "guarded_arm",
			src: `function main(): i32 {
    let r: Reader = stdin();
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        match (r.read_chunk(16)) {
            Ok(big) when big.len() > 8 => { acc = acc + big.len(); },
            Ok(chunk) => { acc = acc + 1; },
            Err(e) => { return 9; }
        }
        i = i + 1;
    }
    return acc + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 40),
			want:  34,
		},
		{
			name: "stored_to_outer_local",
			src: `function main(): i32 {
    let r: Reader = stdin();
    let last: string = "";
    let i: i32 = 0;
    while (i < 4) {
        match (r.read_chunk(16)) {
            Ok(chunk) => { last = chunk; },
            Err(e) => { return 9; }
        }
        i = i + 1;
    }
    return last.len() + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 64),
			want:  16,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			bin := buildBin(t, gcc, dir, "hazard_"+tc.name, asm)
			_, exit := runWithStdin(t, runner, bin, tc.stdin)
			if exit != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, exit, tc.want)
			}
		})
	}
}
