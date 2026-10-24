package e2eharness

import (
	"encoding/json"
	"testing"
)

type FipHttpReport struct {
	Variant          string `json:"variant"`
	Requests         int64  `json:"requests"`
	Ok               int64  `json:"ok"`
	Rejected         int64  `json:"rejected"`
	BadRequestLine   int64  `json:"bad_request_line"`
	NoVersion        int64  `json:"no_version"`
	TransferEncoding int64  `json:"transfer_encoding"`
	DuplicateLength  int64  `json:"duplicate_length"`
	BadLength        int64  `json:"bad_length"`
	Truncated        int64  `json:"truncated"`
	Digest           int64  `json:"digest"`
	ResponseBytes    int64  `json:"response_bytes"`
	SteadyAllocs     int64  `json:"steady_allocs"`
	P50              int64  `json:"round_p50_ns"`
	P999             int64  `json:"round_p999_ns"`
}

func ParseFipHttpReport(t *testing.T, variant string, out []byte) FipHttpReport {
	t.Helper()
	var got FipHttpReport
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

func CheckFipHttpReports(t *testing.T, baseline, fip FipHttpReport) {
	t.Helper()
	if fip.SteadyAllocs != 0 {
		t.Errorf("fip: %d allocations over %d requests, want 0 — the steady state is no longer allocation-free",
			fip.SteadyAllocs, fip.Requests)
	}
	if baseline.SteadyAllocs == 0 {
		t.Errorf("baseline allocated nothing: the experiment has lost its control, and the fip variant's zero means nothing")
	}

	if fip.Requests != baseline.Requests || fip.Ok != baseline.Ok || fip.Rejected != baseline.Rejected {
		t.Errorf("fip disagrees with the baseline:\n  fip: %+v\n  baseline: %+v", fip, baseline)
	}
	if fip.Digest != baseline.Digest || fip.ResponseBytes != baseline.ResponseBytes {
		t.Errorf("fip framed different response bytes: digest %d over %d bytes, baseline %d over %d",
			fip.Digest, fip.ResponseBytes, baseline.Digest, baseline.ResponseBytes)
	}

	classes := []struct {
		name string
		n    int64
	}{
		{"bad_request_line", fip.BadRequestLine}, {"no_version", fip.NoVersion},
		{"transfer_encoding", fip.TransferEncoding}, {"duplicate_length", fip.DuplicateLength},
		{"bad_length", fip.BadLength}, {"truncated", fip.Truncated},
	}
	var sum int64
	for _, c := range classes {
		if c.n == 0 {
			t.Errorf("no request was refused as %q: the malformed-input coverage is gone", c.name)
		}
		sum += c.n
	}
	if sum != fip.Rejected || fip.Ok == 0 {
		t.Errorf("the six classes sum to %d refusals, the report says %d refused and %d served", sum, fip.Rejected, fip.Ok)
	}

	for _, r := range []FipHttpReport{baseline, fip} {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: round p50 %d ns, p99.9 %d ns — the latency report is not a report", r.Variant, r.P50, r.P999)
		}
	}
}
