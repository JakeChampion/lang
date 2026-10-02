package e2e

import (
	"encoding/json"
	"testing"
)

// These benchmarks print both their answers and measurements. Compare every
// answer field across backends, excluding only elapsed time and the allocating
// controls' optimization-dependent counts. Keep the fip allocation counts:
// their zero is part of that program's contract.
func ssaDiffStableStdout(path, stdout string) string {
	switch path {
	case "examples/fip/kv_baseline.fern", "examples/fip/kv_pmap.fern", "examples/fip/kv_fip.fern":
	default:
		return stdout
	}
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &report); err != nil || report == nil {
		return stdout
	}
	metrics := []string{"ns_per_request", "requests_per_sec", "round_p50_ns", "round_p95_ns", "round_p99_ns", "round_p999_ns", "round_max_ns"}
	if path != "examples/fip/kv_fip.fern" {
		metrics = append(metrics, "steady_allocs", "steady_fresh_bytes", "allocs_per_request_milli")
	}
	for _, key := range metrics {
		value, ok := report[key]
		var number int64
		if !ok || string(value) == "null" || json.Unmarshal(value, &number) != nil || number < 0 {
			return stdout
		}
	}
	for _, key := range metrics {
		delete(report, key)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return stdout
	}
	return string(encoded)
}

func TestSSADiffBenchmarkAnswers(t *testing.T) {
	const original = `{"digest":416512069,"requests":640000,"ok":205445,"ns_per_request":100,"requests_per_sec":10000000,"round_p50_ns":6400,"round_p95_ns":6500,"round_p99_ns":6600,"round_p999_ns":6700,"round_max_ns":6800,"steady_allocs":0,"steady_fresh_bytes":0,"allocs_per_request_milli":0}`
	for _, tc := range []struct {
		name, path, key string
		value           any
		equal           bool
	}{
		{"timing", "kv_fip", "round_p99_ns", 9000, true},
		{"baseline allocations", "kv_baseline", "steady_allocs", 100, true},
		{"pmap allocations", "kv_pmap", "steady_fresh_bytes", 1024, true},
		{"fip must not allocate", "kv_fip", "steady_allocs", 1, false},
		{"answer mismatch", "kv_baseline", "digest", 1, false},
		{"request mismatch", "kv_pmap", "requests", 1, false},
		{"unknown answer", "kv_pmap", "new_answer", 1, false},
		{"invalid metric", "kv_pmap", "round_p99_ns", "broken", false},
		{"null metric", "kv_pmap", "round_p99_ns", nil, false},
		{"negative metric", "kv_pmap", "round_p99_ns", -1, false},
		{"other program", "other", "round_p99_ns", 9000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed map[string]any
			if err := json.Unmarshal([]byte(original), &changed); err != nil {
				t.Fatal(err)
			}
			changed[tc.key] = tc.value
			output, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			path := "examples/fip/" + tc.path + ".fern"
			equal := ssaDiffStableStdout(path, original) == ssaDiffStableStdout(path, string(output))
			if equal != tc.equal {
				t.Fatalf("equal = %v, want %v", equal, tc.equal)
			}
		})
	}
}
