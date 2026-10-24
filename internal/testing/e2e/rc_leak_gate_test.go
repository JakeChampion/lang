package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type rcCase struct{ name, src string }

func rcCorpusCases(t *testing.T) []rcCase {
	t.Helper()
	dirs, err := filepath.Glob("../../../conformance/cases/rc_corpus_*")
	if err != nil {
		t.Fatalf("glob rc corpus: %v", err)
	}
	if len(dirs) < 300 {
		t.Fatalf("found %d rc_corpus_* conformance cases; the glob is wrong", len(dirs))
	}
	cases := make([]rcCase, 0, len(dirs))
	for _, d := range dirs {
		src, err := os.ReadFile(filepath.Join(d, "main.fern"))
		if err != nil {
			t.Fatalf("read %s: %v", d, err)
		}
		cases = append(cases, rcCase{strings.TrimPrefix(filepath.Base(d), "rc_corpus_"), string(src)})
	}
	return cases
}

var rcCorpusLeakBaselineX86_64 = map[string]int64{}

var rcCorpusLeakBaselineArm64 = map[string]int64{}

var rcCorpusLeakBaselineWasm = map[string]int64{}

func checkCorpusLeaks(t *testing.T, backend string, baseline map[string]int64,
	run func(*testing.T, string) (string, string, int)) {
	for _, c := range rcCorpusCases(t) {
		t.Run(c.name, func(t *testing.T) {
			_, stderr, code := run(t, c.src)
			re := leakCheckLineRe
			if backend == "wasm" {
				re = wasmLeakCheckLineRe
			}
			m := re.FindStringSubmatch(stderr)
			if m == nil {
				t.Fatalf("%s: no leakcheck report on stderr (exit %d): %q — "+
					"the detector must print exactly one line at the exit seam", c.name, code, stderr)
			}
			live, err := strconv.ParseInt(m[3], 10, 64)
			if err != nil {
				t.Fatalf("%s: unparseable live_bytes %q: %v", c.name, m[3], err)
			}
			want := baseline[c.name]
			switch {
			case live == want:
			case want == 0:
				t.Errorf("%s (%s): leaks %d bytes at exit, want 0 — this case reclaimed everything "+
					"before this change. Fix the drop path; do not add it to the baseline table",
					c.name, backend, live)
			case live > want:
				t.Errorf("%s (%s): leaks %d bytes at exit, up from the pinned %d — "+
					"this case already leaked and now leaks more", c.name, backend, live, want)
			default:
				t.Errorf("%s (%s): leaks %d bytes at exit, down from the pinned %d — "+
					"a leak got fixed. Bank it: set the entry to %d, or delete it if it is now 0",
					c.name, backend, live, want, live)
			}
		})
	}
}

func TestX86_64RcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "x86-64", rcCorpusLeakBaselineX86_64, runLeakCheckX86_64)
}

func TestArm64RcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "arm64", rcCorpusLeakBaselineArm64, runLeakCheckArm64)
}

func TestWASMRcCorpusLeakGate(t *testing.T) {
	checkCorpusLeaks(t, "wasm", rcCorpusLeakBaselineWasm,
		func(t *testing.T, src string) (string, string, int) {
			return runLeakCheckWasm(t, src, false)
		})
}

func TestRcCorpusLeakBaselinesNameRealCases(t *testing.T) {
	have := map[string]bool{}
	for _, c := range rcCorpusCases(t) {
		have[c.name] = true
	}
	for _, tbl := range []struct {
		name string
		m    map[string]int64
	}{
		{"rcCorpusLeakBaselineX86_64", rcCorpusLeakBaselineX86_64},
		{"rcCorpusLeakBaselineArm64", rcCorpusLeakBaselineArm64},
		{"rcCorpusLeakBaselineWasm", rcCorpusLeakBaselineWasm},
	} {
		var stale []string
		for name := range tbl.m {
			if !have[name] {
				stale = append(stale, name)
			}
		}
		sort.Strings(stale)
		if len(stale) > 0 {
			t.Errorf("%s names %d case(s) no longer in the rc corpus: %s — delete the entries",
				tbl.name, len(stale), strings.Join(stale, ", "))
		}
	}
}
