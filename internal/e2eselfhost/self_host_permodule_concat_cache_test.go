package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostPerModuleConcatObjectCacheX86_64 drives `-cache-dir` through the
// single-process per-module concat (the 512–1500 function band), #6937. Every
// phase is compared byte-for-byte with a clean concat build of the same sources.
//
// The reach phase is the one a source-and-signature key gets wrong: the entry
// starts calling a second lib3 function, so lib3's pruned unit grows although
// lib3.fern is untouched. It must be re-emitted, not served. The fact phase is
// the other: a body-only edit that flips a borrow verdict its caller reads.
func TestSelfHostPerModuleConcatObjectCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir, mmr := buildConcatDriver(t, gcc)
	entryPath, nMod := writeConcatFixture(t, dir)
	proj := filepath.Dir(entryPath)
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	drive := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(mmr, append([]string{entryPath}, args...)...)
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver %v: %v\nstderr:\n%s", args, err, errb.String())
		}
		return string(out), errb.String()
	}
	concat := func(phase string) ([]string, []string) {
		t.Helper()
		clean, _ := drive()
		assertConcatProduced(t, []byte(clean))
		cached, errs := drive("-cache-dir", cacheDir)
		if cached != clean {
			t.Fatalf("%s: cached concat differs from a clean build (%d vs %d bytes)", phase, len(cached), len(clean))
		}
		return pmCacheLines(errs)
	}
	edit := func(name, old, new string) {
		t.Helper()
		p := filepath.Join(proj, name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(b), old) {
			t.Fatalf("%s does not contain %q", name, old)
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	all := []string{"__entry"}
	for m := 0; m < nMod; m++ {
		all = append(all, "lib"+string(rune('0'+m)))
	}
	without := func(ns string) []string {
		var out []string
		for _, a := range all {
			if a != ns {
				out = append(out, a)
			}
		}
		return out
	}

	hits, misses := concat("cold")
	pmWantSets(t, "cold", hits, misses, []string{}, all)

	hits, misses = concat("warm")
	pmWantSets(t, "warm", hits, misses, all, []string{})

	// Body-only edit of a function the reach set drops: lib3's source changes,
	// so lib3 alone re-emits.
	edit("lib3.fern", "return x + 305;", "return x + 1305;")
	hits, misses = concat("body")
	pmWantSets(t, "body", hits, misses, without("lib3"), []string{"lib3"})

	// Reach edit: the entry keeps its value but now reaches m3_f1.
	edit("entry.fern", "lib3.m3_f0(1)", "lib3.m3_f0(1) + lib3.m3_f1(1) - lib3.m3_f1(1)")
	hits, misses = concat("reach")
	var reHits []string
	for _, a := range all {
		if a != "__entry" && a != "lib3" {
			reHits = append(reHits, a)
		}
	}
	pmWantSets(t, "reach", hits, misses, reHits, []string{"__entry", "lib3"})

	// A body-only edit that moves a fact about a function. m3_keep's parameter is
	// borrowable until its body starts storing it, and callers lower against that
	// verdict, so lib3's facts move and every module importing lib3 re-emits the
	// way it would for a signature change. Modules outside that closure are
	// served. First bring m3_keep into reach, then make the fact-only edit.
	edit("lib3.fern", "return x + 301;", "return x + 301 + m3_keep([x]) - 1;")
	b3, err := os.ReadFile(filepath.Join(proj, "lib3.fern"))
	if err != nil {
		t.Fatalf("read lib3: %v", err)
	}
	keep := "pub function m3_keep(xs: i32[]): i32 { return xs.len(); }\n"
	if err := os.WriteFile(filepath.Join(proj, "lib3.fern"), append(b3, keep...), 0o644); err != nil {
		t.Fatalf("write lib3: %v", err)
	}
	concat("keep")
	edit("lib3.fern", "{ return xs.len(); }", "{ var h: i32[][] = [xs]; return h[0].len(); }")
	hits, misses = concat("fact")
	pmWantSets(t, "fact", hits, misses, reHits, []string{"__entry", "lib3"})

	asm, _ := drive("-cache-dir", cacheDir)
	bin := buildBin(t, gcc, dir, "concat_cache_prog", asm)
	rc := exec.Command(bin)
	_ = rc.Run()
	if code := rc.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("cached concat program exited %d, want 0", code)
	}

	// The concat cache is warm for these sources; an emit-all run against the
	// same directory must not be served any of its units, and a concat run
	// against a directory emit-all populated must not be served either.
	outDir := filepath.Join(proj, "ea_out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, errs := drive("-per-module-emit-all", "-out-dir", outDir, "-assume-eligible", "-cache-dir", cacheDir)
	if eaHits, _ := pmCacheLines(errs); len(eaHits) != 0 {
		t.Fatalf("emit-all was served concat units: %v", eaHits)
	}
	eaOnly := filepath.Join(proj, "ea_cache")
	if err := os.MkdirAll(eaOnly, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	drive("-per-module-emit-all", "-out-dir", outDir, "-assume-eligible", "-cache-dir", eaOnly)
	_, errs = drive("-cache-dir", eaOnly)
	if cHits, _ := pmCacheLines(errs); len(cHits) != 0 {
		t.Fatalf("concat was served emit-all units: %v", cHits)
	}
}
