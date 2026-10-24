package e2ecompiler

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostPerModuleObjectCacheArm64(t *testing.T) {
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_modload_run.fern", "arm64cachedriver")

	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	writeLeaf := func(body string) {
		if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte(body), 0o644); err != nil {
			t.Fatalf("write leaf.fern: %v", err)
		}
	}
	writeLeaf("@noinline pub function leaf_val(): i32 { return 40; }\n")
	if err := os.WriteFile(filepath.Join(proj, "mid.fern"), []byte(
		"import \"./leaf\";\n"+
			"@noinline pub function mid_val(): i32 {\n"+
			"    let x = leaf.leaf_val();\n"+
			"    if (x > 0) { return 42; }\n"+
			"    return 0;\n"+
			"}\n"), 0o644); err != nil {
		t.Fatalf("write mid.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.fern"), []byte(
		"import \"./mid\";\nfunction main(): i32 { return mid.mid_val(); }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	entry := filepath.Join(proj, "main.fern")

	drive := func(args ...string) (string, string) {
		t.Helper()
		cmd := runX86_64Bin(x86runner, driverBin, append([]string{entry, "-target", "arm64-linux"}, args...)...)
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver %v: %v\nstderr:\n%s", args, err, errb.String())
		}
		return string(out), errb.String()
	}

	var nsOrder []string
	for _, ln := range strings.Split(mustStdout(drive("-per-module-manifest")), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			nsOrder = append(nsOrder, strings.SplitN(ln, "|", 2)[0])
		}
	}
	if len(nsOrder) < 3 {
		t.Fatalf("expected >=3 modules, got %v", nsOrder)
	}

	var needArgs []string
	for _, ln := range strings.Split(mustStdout(drive("-per-module-needs")), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	buildWithCache := func() (map[string]string, []string, []string) {
		units := map[string]string{}
		var hits, misses []string
		for i, ns := range nsOrder {
			args := append([]string{"-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir}, needArgs...)
			out, errs := drive(args...)
			if !strings.Contains(out, "ret") {
				t.Fatalf("unit %q does not look like arm64 asm", ns)
			}
			units[ns] = out
			for _, ln := range strings.Split(errs, "\n") {
				ln = strings.TrimSpace(ln)
				if m := strings.TrimPrefix(ln, "cache-hit "); m != ln {
					hits = append(hits, m)
				} else if m := strings.TrimPrefix(ln, "cache-miss "); m != ln {
					misses = append(misses, m)
				}
			}
		}
		sort.Strings(hits)
		sort.Strings(misses)
		return units, hits, misses
	}

	eq := func(a []string, want ...string) bool {
		b := append([]string(nil), want...)
		sort.Strings(b)
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	units, hits, misses := buildWithCache()
	if len(hits) != 0 || !eq(misses, nsOrder...) {
		t.Fatalf("cold: hits=%v misses=%v, want no hits and all-miss", hits, misses)
	}

	units2, hits2, misses2 := buildWithCache()
	if !eq(hits2, nsOrder...) || len(misses2) != 0 {
		t.Fatalf("warm: hits=%v misses=%v, want all-hit and no miss", hits2, misses2)
	}
	for _, ns := range nsOrder {
		if units2[ns] != units[ns] {
			t.Errorf("warm: arm64 unit %q changed despite no edit", ns)
		}
	}

	writeLeaf("@noinline pub function leaf_val(): i32 { return 41; }\n")
	unitsA, hitsA, missesA := buildWithCache()
	if !eq(missesA, "leaf") || !eq(hitsA, "mid", "__entry") {
		t.Fatalf("body edit: misses=%v hits=%v, want misses=[leaf] hits=[mid __entry]", missesA, hitsA)
	}
	for _, ns := range []string{"mid", "__entry"} {
		if unitsA[ns] != units[ns] {
			t.Errorf("body edit: arm64 unit %q not reused byte-identically", ns)
		}
	}

	writeLeaf("@noinline pub function leaf_val(): i64 { return 41i64; }\n")
	_, hitsB, missesB := buildWithCache()
	if !eq(missesB, "leaf", "mid", "__entry") || len(hitsB) != 0 {
		t.Fatalf("sig edit: misses=%v hits=%v, want misses=[leaf mid __entry] hits=[]", missesB, hitsB)
	}
}
