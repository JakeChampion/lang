package e2e

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	runner := x86NativeRunner(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()

	baseline := runFipPacket(t, fern, dir, "baseline", "baseline", runner)
	borrowed := runFipPacket(t, fern, dir, "borrowed", "borrowed", runner)
	fbip := runFipPacket(t, fern, dir, "fbip", "fbip", runner)
	inplace := runFipPacket(t, fern, dir, "fip", "fip", runner)
	separate := runFipPacket(t, fern, dir, "fip", "fip-separate", runner, "separate")
	all := []fipPacketReport{baseline, borrowed, fbip, inplace, separate}

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

	if !(baseline.CopiedPerReq > borrowed.CopiedPerReq && borrowed.CopiedPerReq > fbip.CopiedPerReq) {
		t.Errorf("copying did not fall along the ladder: baseline %d, borrowed %d, fbip %d",
			baseline.CopiedPerReq, borrowed.CopiedPerReq, fbip.CopiedPerReq)
	}
	if inplace.CopiedPerReq >= separate.CopiedPerReq {
		t.Errorf("framing the response over the request copied %d bytes per request, no better than the separate output buffer's %d — the in-place path has stopped being in place",
			inplace.CopiedPerReq, separate.CopiedPerReq)
	}

	for _, r := range all {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: p50=%d p99.9=%d is not a distribution", r.Variant, r.P50, r.P999)
		}
	}
}
