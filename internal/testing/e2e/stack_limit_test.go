package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunWithStackLimit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux per-process core filter")
	}
	parent, err := os.ReadFile("/proc/self/coredump_filter")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		expectOverflow bool
		filter         string
		finish         string
	}{
		{"successful", false, strings.TrimSpace(string(parent)), "exit 0"},
		{"expected_crash", true, "00000000", "kill -SEGV \"$$\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "probe")
			script := fmt.Sprintf("#!/bin/bash\n[ \"$(ulimit -S -s)\" = 16384 ] || exit 51\nread -r filter < /proc/self/coredump_filter\n[ \"$filter\" = %q ] || exit 52\n%s\n", tc.filter, tc.finish)
			if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			code := runWithStackLimit(t, 16*1024, bin, tc.expectOverflow)
			if !tc.expectOverflow && code != 0 {
				t.Fatalf("successful probe exited %d", code)
			}
			after, err := os.ReadFile("/proc/self/coredump_filter")
			if err != nil || string(after) != string(parent) {
				t.Fatalf("parent filter changed: before %q, after %q, error %v", parent, after, err)
			}
		})
	}
}
