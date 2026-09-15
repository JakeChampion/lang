package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func event(action, name string, seconds int) string {
	stamp := time.Date(2026, 9, 15, 0, 0, seconds, 0, time.UTC).Format(time.RFC3339Nano)
	return fmt.Sprintf("{\"Time\":%q,\"Action\":%q,\"Package\":\"example/pkg\",\"Test\":%q,\"Elapsed\":0.42}\n", stamp, action, name)
}

func TestDurations(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        map[string]float64
		err         string
	}{
		{
			name: "parallel children are inside the measured lifetime",
			input: event("run", "TestParent", 0) + event("run", "TestParent/child", 1) +
				event("pause", "TestParent/child", 2) + event("cont", "TestParent/child", 3) +
				event("pass", "TestParent/child", 70) + event("pass", "TestParent", 71),
			want: map[string]float64{"TestParent": 71},
		},
		{
			name: "exclude top-level scheduler pause",
			input: event("run", "TestParallel", 0) + event("pause", "TestParallel", 1) +
				event("cont", "TestParallel", 11) + event("pass", "TestParallel", 20),
			want: map[string]float64{"TestParallel": 10},
		},
		{
			name: "cached package cannot replace measured weights",
			input: event("run", "TestCached", 0) + event("pass", "TestCached", 1) +
				"{\"Action\":\"output\",\"Package\":\"example/pkg\",\"Output\":\"ok  \\texample/pkg\\t(cached)\\n\"}\n",
			want: map[string]float64{},
		},
		{
			name: "unfinished tests are not measurements",
			input: event("run", "TestA", 0) + event("pass", "TestA", 2) +
				event("run", "TestInterrupted", 3),
			want: map[string]float64{"TestA": 2},
		},
		{
			name: "repeated observations retain maximum",
			input: event("run", "TestA", 0) + event("pass", "TestA", 2) +
				event("run", "TestA", 3) + event("fail", "TestA", 6),
			want: map[string]float64{"TestA": 3},
		},
		{
			name: "nanoseconds across dates and offsets",
			input: "{\"Time\":\"2026-09-15T23:59:59.123456789Z\",\"Action\":\"run\",\"Test\":\"TestTime\"}\n" +
				"{\"Time\":\"2026-09-16T01:00:00.623456789+01:00\",\"Action\":\"pass\",\"Test\":\"TestTime\"}\n",
			want: map[string]float64{"TestTime": 1.5},
		},
		{name: "terminal only", input: event("pass", "TestA", 1), err: "missing run event"},
		{name: "duplicate start", input: event("run", "TestA", 0) + event("run", "TestA", 1), err: "duplicate start"},
		{name: "backwards clock", input: event("run", "TestA", 1) + event("pass", "TestA", 0), err: "backwards"},
		{name: "missing time", input: `{"Action":"run","Test":"TestA"}`, err: "missing timestamp"},
		{name: "invalid resume", input: event("run", "TestA", 0) + event("cont", "TestA", 1), err: "invalid resume"},
		{name: "paused completion", input: event("run", "TestA", 0) + event("pause", "TestA", 1) + event("pass", "TestA", 2), err: "while paused"},
		{name: "malformed JSON", input: `{"Action":`, err: "invalid test JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := durations(strings.NewReader(tc.input))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("durations = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestExtractFiles(t *testing.T) {
	var files []string
	for i, seconds := range []int{2, 3} {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("%d.json", i))
		if err := os.WriteFile(path, []byte(event("run", "TestA", 0)+event("pass", "TestA", seconds)), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	got, err := extract(files)
	if err != nil || got["TestA"] != 3 {
		t.Fatalf("extract = %v, %v", got, err)
	}
	if _, err := extract(nil); err == nil {
		t.Error("accepted no files")
	}
	if _, err := extract([]string{filepath.Join(t.TempDir(), "missing.json")}); err == nil {
		t.Error("accepted missing file")
	}
}
