package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// The bounded packet codec of #9584, in its four memory disciplines: an
// allocating record parse, a sliced parse that borrows the wire buffer, an
// `fbip` struct of buffers, and a strict `fip` data plane over one owned buffer
// (run twice — framing the response over the request, and into a second
// preallocated buffer). `docs/FIP-PACKET-PROTOCOL.md` reports what they
// measure; this pins the claims that would make that report false.
//
//  1. The disciplined variants allocate NOTHING in steady state, over 256,000
//     requests, and the two allocating ones do allocate. Without the second
//     half the comparison is between two things that are the same.
//  2. All five agree, request for request AND byte for byte. The digest is a
//     running hash over every response byte, so a variant that framed a
//     different answer is caught rather than reporting a throughput win for
//     doing less work.
//  3. Every rejection class fires in the steady state. The workload corrupts
//     every 17th request, cycling through all six, so the validation ladder is
//     exercised by the thing being measured rather than by a separate test.
//  4. Copying falls monotonically along the ladder. That is the measurement
//     the allocation counter cannot make: the in-place `fip` mode moves fewer
//     bytes than any other variant because an echo's answer is already in the
//     buffer, and both `fip` modes read zero allocations either way.

type fipPacketReport struct {
	Variant      string `json:"variant"`
	Requests     int64  `json:"requests"`
	Ok           int64  `json:"ok"`
	BadMagic     int64  `json:"bad_magic"`
	BadVersion   int64  `json:"bad_version"`
	BadType      int64  `json:"bad_type"`
	Oversize     int64  `json:"oversize"`
	Truncated    int64  `json:"truncated"`
	BadChecksum  int64  `json:"bad_checksum"`
	Digest       int64  `json:"digest"`
	BytesCopied  int64  `json:"bytes_copied"`
	CopiedPerReq int64  `json:"bytes_copied_per_request"`
	SteadyAllocs int64  `json:"steady_allocs"`
	P50          int64  `json:"round_p50_ns"`
	P999         int64  `json:"round_p999_ns"`
}

func runFipPacket(t *testing.T, fern, dir, source, variant string, runner []string, args ...string) fipPacketReport {
	t.Helper()
	src := langSrcAbs(t, filepath.Join("examples", "fip", "packet_"+source+".fern"))
	bin := filepath.Join(dir, source)
	if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile %s: %v\n%s", source, err, out)
	}
	// The binary is x86-64 whatever the host is, so it goes through the runner
	// x86NativeRunner supplies — directly on amd64, under qemu-x86_64 elsewhere.
	out, err := benchX86Cmd(runner, bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %v: %v\n%s", source, args, err, out)
	}
	var got fipPacketReport
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s printed no report: %v\n%s", variant, err, out)
	}
	if got.Variant != variant {
		t.Fatalf("report names variant %q, ran %q", got.Variant, variant)
	}
	if got.Requests == 0 {
		t.Fatalf("%s reports no requests", variant)
	}
	return got
}

func TestFipPacketDisciplinesAgreeAndDoNotAllocate(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	fern := buildFernCLI(t)
	dir := t.TempDir()

	baseline := runFipPacket(t, fern, dir, "baseline", "baseline", runner)
	borrowed := runFipPacket(t, fern, dir, "borrowed", "borrowed", runner)
	fbip := runFipPacket(t, fern, dir, "fbip", "fbip", runner)
	inplace := runFipPacket(t, fern, dir, "fip", "fip", runner)
	separate := runFipPacket(t, fern, dir, "fip", "fip-separate", runner, "separate")
	all := []fipPacketReport{baseline, borrowed, fbip, inplace, separate}

	// 1. The disciplined variants allocate nothing; the controls allocate.
	for _, r := range []fipPacketReport{fbip, inplace, separate} {
		if r.SteadyAllocs != 0 {
			t.Errorf("%s: %d allocations over %d requests, want 0 — the steady state is no longer allocation-free",
				r.Variant, r.SteadyAllocs, r.Requests)
		}
	}
	for _, r := range []fipPacketReport{baseline, borrowed} {
		if r.SteadyAllocs == 0 {
			t.Errorf("%s allocated nothing: the experiment has lost its control, and the other three variants' zero means nothing", r.Variant)
		}
	}

	// 2. All five agree, request for request and byte for byte.
	for _, r := range all[1:] {
		if r.Requests != baseline.Requests || r.Ok != baseline.Ok || r.BadMagic != baseline.BadMagic ||
			r.BadVersion != baseline.BadVersion || r.BadType != baseline.BadType ||
			r.Oversize != baseline.Oversize || r.Truncated != baseline.Truncated ||
			r.BadChecksum != baseline.BadChecksum {
			t.Errorf("%s disagrees with the baseline:\n  %s: %+v\n  baseline: %+v", r.Variant, r.Variant, r, baseline)
		}
		if r.Digest != baseline.Digest {
			t.Errorf("%s framed different response bytes: digest %d, baseline %d", r.Variant, r.Digest, baseline.Digest)
		}
	}

	// 3. Every rejection class fires, so the ladder is covered by the steady
	//    state. A zero here means the generator stopped corrupting and the
	//    malformed-input coverage silently went away.
	for _, c := range []struct {
		name string
		n    int64
	}{
		{"ok", baseline.Ok}, {"bad_magic", baseline.BadMagic}, {"bad_version", baseline.BadVersion},
		{"bad_type", baseline.BadType}, {"oversize", baseline.Oversize},
		{"truncated", baseline.Truncated}, {"bad_checksum", baseline.BadChecksum},
	} {
		if c.n == 0 {
			t.Errorf("no request was classified %q: the malformed-input coverage is gone", c.name)
		}
	}

	// 4. Copying falls along the ladder, and the in-place mode moves least.
	if !(baseline.CopiedPerReq > borrowed.CopiedPerReq && borrowed.CopiedPerReq > fbip.CopiedPerReq) {
		t.Errorf("copying did not fall along the ladder: baseline %d, borrowed %d, fbip %d",
			baseline.CopiedPerReq, borrowed.CopiedPerReq, fbip.CopiedPerReq)
	}
	if inplace.CopiedPerReq >= separate.CopiedPerReq {
		t.Errorf("framing the response over the request copied %d bytes per request, no better than the separate output buffer's %d — the in-place path has stopped being in place",
			inplace.CopiedPerReq, separate.CopiedPerReq)
	}

	// 5. Every variant reports a tail, so the latency half cannot silently
	//    become zeros.
	for _, r := range all {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: p50=%d p99.9=%d is not a distribution", r.Variant, r.P50, r.P999)
		}
	}
}
