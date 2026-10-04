package e2eharness

import (
	"encoding/json"
	"testing"
)

// The HTTP-like application pipeline of #9586 (experiment 4) in its three
// memory disciplines: the service through `std/http` and a `Map`, the same
// service as an `fbip` plane with the response buffer inside the state, and
// as a two-call `fip` plane (`examples/fip/httpapp_{baseline,fbip,fip}.fern`).
// `docs/FIP-HTTP-APP.md` reports what they measure; CheckFipHttpAppReports
// pins the claims that would make that report false.

// FipHttpAppReport is the JSON line each variant prints. The six refusal
// classes are reported by the two disciplined variants only: the baseline
// sees a parse failure, not which rule refused.
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

// ParseFipHttpAppReport reads the report `variant` printed.
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

// CheckFipHttpAppReports pins the experiment's claims:
//
//  1. The `fip` and `fbip` variants allocate NOTHING in steady state, over
//     128,000 requests with the table full for most of them, and the
//     baseline does allocate. Without the last the comparison is between
//     things that are the same.
//  2. All three agree, request for request AND byte for byte: the same
//     count in every class, the same final table, and the same digest over
//     every response byte, so a variant that framed a different answer is
//     caught rather than reporting a throughput win for doing less work.
//  3. The bounds are exercised by the thing being measured: the table
//     fills and the overflow is answered rather than absorbed, both limits
//     refuse, both application errors fire, and every one of the six
//     refusal classes fires in the disciplined variants and together they
//     are every refusal.
//  4. Every variant reports a tail, so the latency half cannot silently go
//     missing.
func CheckFipHttpAppReports(t *testing.T, baseline, fbip, fip FipHttpAppReport) {
	t.Helper()
	// 1. The disciplined variants allocate nothing; the control allocates.
	for _, r := range []FipHttpAppReport{fbip, fip} {
		if r.SteadyAllocs != 0 {
			t.Errorf("%s: %d allocations over %d requests, want 0 — the steady state is no longer allocation-free",
				r.Variant, r.SteadyAllocs, r.Requests)
		}
	}
	if baseline.SteadyAllocs == 0 {
		t.Errorf("baseline allocated nothing: the experiment has lost its control, and the zeros mean nothing")
	}

	// 2. All three agree, request for request and byte for byte.
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

	// 3. The bounds are exercised. The table holds 256 counters and the
	// workload names 320 ids, so it must fill and must refuse.
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

	// 4. Every variant reports a tail, so the latency half cannot silently go missing.
	for _, r := range []FipHttpAppReport{baseline, fbip, fip} {
		if r.P50 <= 0 || r.P999 < r.P50 {
			t.Errorf("%s: round p50 %d ns, p99.9 %d ns — the latency report is not a report", r.Variant, r.P50, r.P999)
		}
	}
}
