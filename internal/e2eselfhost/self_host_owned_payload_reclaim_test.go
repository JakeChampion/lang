package e2eselfhost

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// Self-host RC: a match over read_chunk / read_line owns the payload it binds
// (#8402, the self-host half of native's #8396 / #8399).
//
// The runtime hands back an IMMORTAL Option / Result box whose success payload
// is a fresh rc=1 string built for this caller. The box needs no release; the
// payload's unit is the caller's, and nothing released it: the arm binding was a
// borrow of a box nothing sweeps, so every chunk of a streaming-reader loop
// leaked. FERN_LEAKCHECK on 20 MB of stdin through the pass-through loop read
// allocs=461 frees=0 live_bytes=20,201,064.
//
// The two shapes each leg pins:
//
//   - RECLAIM. The same call count at a 64x chunk size must cost the SAME
//     live bytes. That separates the payload from the per-call immortal box,
//     which is a constant a doubling test cannot tell from a leak (the trap
//     docs/rc-log/2026-09-05-owned-payload-builtins.md records). With the
//     payloads leaking the wide round costs 64x the narrow one.
//   - The runtime's own half: a SHORT read is copied into an exact-size block
//     and the oversized one given back, so a pipe — 64 KiB a read against a
//     larger request — recycles instead of stranding the slack below the
//     freelist class the block lands in.
//
// The HAZARD group is the other direction: a binding that escapes its arm keeps
// today's leak rather than being released under the destination's own claim, so
// each of those must still compute the right answer and not over-release.

// readChunkDrainSrc drains `rounds` chunks of `size` bytes and returns the
// summed length modulo 251, so a wrong answer is an exit code rather than a
// silent pass.
func readChunkDrainSrc(size, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var r: Reader = stdin();
    var acc: i32 = 0;
    var i: i32 = 0;
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

// readChunkDiscardSrc is the same drain with the payload DISCARDED: `Ok(_)` at
// an owned position has no binding to own the payload, so the arm releases it on
// the spot.
func readChunkDiscardSrc(size, rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var r: Reader = stdin();
    var i: i32 = 0;
    while (i < %d) {
        match (r.read_chunk(%d)) {
            Ok(_) => { i = i + 1; },
            Err(e) => { return 9; }
        }
    }
    return 0;
}`, rounds, size)
}

// readLineDrainSrc drains `rounds` lines. read_line's own buffer is a fixed 256
// bytes whatever the line length, so the size axis here is the LINE, and the
// same equality holds: a longer line must not cost more live bytes.
func readLineDrainSrc(rounds int) string {
	return fmt.Sprintf(`function main(): i32 {
    var r: Reader = stdin();
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < %d) {
        match (r.read_line()) {
            Some(line) => { acc = acc + line.len(); },
            None => { return 9; }
        }
        i = i + 1;
    }
    return acc %% 251;
}`, rounds)
}

// runWithStdin runs a built binary against `stdin` and returns its stderr and
// exit code. hevRun gives the child no stdin at all, and a reader probe needs
// bytes to read.
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

// leakSummaryOf pulls the leakcheck triple out of a program's stderr.
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

// TestSelfHostOwnedPayloadReclaimX86_64 — the reclaim leg.
func TestSelfHostOwnedPayloadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	const rounds = 24
	// One line per round for the read_line probe; a wide input must not
	// multiply the CALL count, or the per-call box constant "scales" on its
	// own (the #8396 measurement trap).
	lines := func(width int) []byte {
		var b bytes.Buffer
		for i := 0; i < rounds; i++ {
			b.Write(bytes.Repeat([]byte{'a'}, width))
			b.WriteByte('\n')
		}
		return b.Bytes()
	}

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
		{
			name:     "read_line_bound",
			narrow:   readLineDrainSrc(rounds),
			wide:     readLineDrainSrc(rounds),
			inNarrow: lines(3),
			inWide:   lines(200),
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
			// The reclaim shape: 64x the payload bytes at the same call count
			// must cost the same live bytes. A leaked payload makes the wide
			// round ~64x the narrow one.
			if nl != wl {
				t.Errorf("live_bytes narrow=%d wide=%d (allocs %d/%d, frees %d/%d): the owned "+
					"payload must not scale with the chunk size — what is left is the per-call "+
					"immortal Option/Result box, a constant",
					nl, wl, na, wa, nf, wf)
			}
		})
	}
}

// TestSelfHostOwnedPayloadPipeReclaimX86_64 — the runtime half on its own.
//
// A pipe hands back at most 64 KiB a read, so a 128 KiB request is always SHORT.
// Boxing the oversized block at the short length hands the freelist a block
// bigger than the class it lands in, so nothing recycles and the loop bumps a
// fresh chunk per iteration. Reading the SAME bytes from a pipe and from a
// regular file must therefore cost the same live bytes.
func TestSelfHostOwnedPayloadShortReadReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// 24 rounds of 1024 bytes requested; the input holds 24 x 16, so EVERY
	// read is short by 1008 bytes and the answers still match the full-read
	// probe above.
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

// TestSelfHostOwnedPayloadHazardsX86_64 — the shapes the admission must REFUSE.
//
// Each binding leaves the arm, so releasing it at the bind site or at the exit
// sweep would free a value another owner still names. The only safe outcome is
// today's leak, and it is asserted through behaviour: a wrongly-admitted binding
// is a use-after-free, which shows up as a wrong answer, a non-zero underflow
// count, or a crash.
func TestSelfHostOwnedPayloadHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name  string
		src   string
		stdin []byte
		want  int
	}{
		{
			// The payload is APPENDED to an array that outlives the loop and
			// is read back after it — the escape-through-append probe.
			name: "escapes_through_append",
			src: `function main(): i32 {
    var r: Reader = stdin();
    var held: string[] = [];
    var i: i32 = 0;
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
			// The payload is handed to a USER function that KEEPS it — an
			// argument position no registry proves borrowing — and the result
			// is read back after an allocating churn, so a released payload
			// reads as the allocator's filler rather than as luck. Admitting
			// this binding answers 27 instead of 32.
			name: "passed_to_user_fn_that_keeps_it",
			src: `function eat(n: i32): i32 {
    var s: string = "x";
    var i: i32 = 0;
    while (i < n) { s = s + "yyyyyyyyyy"; i = i + 1; }
    return s.len();
}
function keep(t: string): string[] {
    var out: string[] = [];
    out = out.append(t);
    return out;
}
function main(): i32 {
    var r: Reader = stdin();
    var held: string[] = [];
    var i: i32 = 0;
    while (i < 4) {
        match (r.read_chunk(16)) {
            Ok(chunk) => { var one: string[] = keep(chunk); held = held.append(one[0]); },
            Err(e) => { return 9; }
        }
        i = i + 1;
    }
    var junk: i32 = eat(300);
    return held[0].len() + held[3].len() + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 64),
			want:  32,
		},
		{
			// The payload is RETURNED, so its reference leaves the frame.
			name: "returned_from_arm",
			src: `function first(r: Reader): string {
    match (r.read_chunk(16)) {
        Ok(chunk) => { return chunk; },
        Err(e) => { return ""; }
    }
    return "";
}
function main(): i32 {
    var r: Reader = stdin();
    var s: string = first(r);
    return s.len() + __rc_underflow_count();
}`,
			stdin: bytes.Repeat([]byte{'x'}, 64),
			want:  16,
		},
		{
			// A GUARDED arm keeps today's leak: a failed guard falls through to
			// a sibling that re-reads the same payload.
			name: "guarded_arm",
			src: `function main(): i32 {
    var r: Reader = stdin();
    var acc: i32 = 0;
    var i: i32 = 0;
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
			// 40 bytes: two full 16-byte reads take the guarded arm, then an
			// 8-byte one and the EOF read take the unguarded sibling — so both
			// arms bind, which is the shape the guard exists to withhold.
			stdin: bytes.Repeat([]byte{'x'}, 40),
			want:  34,
		},
		{
			// The payload is stored into an outer local that outlives the arm.
			name: "stored_to_outer_local",
			src: `function main(): i32 {
    var r: Reader = stdin();
    var last: string = "";
    var i: i32 = 0;
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
