package e2eharness

import (
	"encoding/json"
	"testing"
)

type FipHttpAppReport struct {
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
	NotFound         int64  `json:"not_found"`
	MethodNotAllowed int64  `json:"method_not_allowed"`
	TooManyHeaders   int64  `json:"too_many_headers"`
	BodyTooLarge     int64  `json:"body_too_large"`
	Full             int64  `json:"full"`
	CountersLive     int64  `json:"counters_live"`
	CounterTotal     int64  `json:"counter_total"`
	Digest           int64  `json:"digest"`
	ResponseBytes    int64  `json:"response_bytes"`
	SteadyAllocs     int64  `json:"steady_allocs"`
	P50              int64  `json:"round_p50_ns"`
	P999             int64  `json:"round_p999_ns"`
}

func ParseFipHttpAppReport(t *testing.T, variant string, out []byte) FipHttpAppReport {
	t.Helper()
	var got FipHttpAppReport
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

func CheckFipHttpAppReports(t *testing.T, baseline, fbip, fip FipHttpAppReport) {
	t.Helper()
	for _, r := range []FipHttpAppReport{fbip, fip} {
		if r.SteadyAllocs != 0 {
			t.Errorf("%s: %d allocations over %d requests, want 0 — the steady state is no longer allocation-free",
				r.Variant, r.SteadyAllocs, r.Requests)
		}
	}
	if baseline.SteadyAllocs == 0 {
		t.Errorf("baseline allocated nothing: the experiment has lost its control, and the zeros mean nothing")
	}

	for _, r := range []FipHttpAppReport{fbip, fip} {
		if r.Requests != baseline.Requests || r.Ok != baseline.Ok || r.Rejected != baseline.Rejected ||
			r.NotFound != baseline.NotFound || r.MethodNotAllowed != baseline.MethodNotAllowed ||
			r.TooManyHeaders != baseline.TooManyHeaders || r.BodyTooLarge != baseline.BodyTooLarge ||
			r.Full != baseline.Full || r.CountersLive != baseline.CountersLive || r.CounterTotal != baseline.CounterTotal {
			t.Errorf("%s disagrees with the baseline:\n  %s: %+v\n  baseline: %+v", r.Variant, r.Variant, r, baseline)
		}
		if r.Digest != baseline.Digest || r.ResponseBytes != baseline.ResponseBytes {
			t.Errorf("%s framed different response bytes: digest %d over %d bytes, baseline %d over %d",
				r.Variant, r.Digest, r.ResponseBytes, baseline.Digest, baseline.ResponseBytes)
		}
	}

	if fip.CountersLive != 256 {
		t.Errorf("the counter table ended with %d live entries, want 256: it never filled, so the overload path was not measured", fip.CountersLive)
	}
	for _, c := range []struct {
		name string
		n    int64
	}{
		{"full", fip.Full}, {"too_many_headers", fip.TooManyHeaders}, {"body_too_large", fip.BodyTooLarge},
		{"not_found", fip.NotFound}, {"method_not_allowed", fip.MethodNotAllowed}, {"ok", fip.Ok},
	} {
		if c.n == 0 {
			t.Errorf("no request was answered as %q: that path is no longer in the steady state", c.name)
		}
	}
	for _, r := range []FipHttpAppReport{fbip, fip} {
		classes := []struct {
			name string
			n    int64
		}{
			{"bad_request_line", r.BadRequestLine}, {"no_version", r.NoVersion},
			{"transfer_encoding", r.TransferEncoding}, {"duplicate_length", r.DuplicateLength},
			{"bad_length", r.BadLength}, {"truncated", r.Truncated},
		}
		var sum int64
		for _, c := range classes {
			if c.n == 0 {
				t.Errorf("%s: no request was refused as %q: the malformed-input coverage is gone", r.Variant, c.name)
			}
			sum += c.n
		}
		if sum != r.Rejected {
			t.Errorf("%s: the six classes sum to %d refusals, the report says %d", r.Variant, sum, r.Rejected)
		}
	}

	for _, r := range []FipHttpAppReport{baseline, fbip, fip} {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: round p50 %d ns, p99.9 %d ns — the latency report is not a report", r.Variant, r.P50, r.P999)
		}
	}
}
