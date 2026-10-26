package e2ecompiler

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostInflateReader(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, fixture := range []string{"deflate_reader_test.fern", "deflate_test.fern"} {
		t.Run(fixture, func(t *testing.T) {
			path := filepath.Join(e2eharness.RepoPath(), "tests", "stdlib", fixture)
			for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if code != 0 {
						t.Fatalf("exit %d: %s", code, stderr)
					}
					var allocs, frees, live int64
					summary := leakSummaryLine(stderr)
					if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
						t.Fatalf("parse census %q: %v", summary, err)
					}
					if allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("invalid census: %s", summary)
					}
				})
			}
		})
	}
}
