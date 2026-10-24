package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func pmCacheTree(t *testing.T, leaf string) (string, string) {
	t.Helper()
	proj := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("leaf.fern", leaf)
	write("mid.fern", "import \"./leaf\";\n"+
		"pub function mid_val(): i32 {\n"+
		"    let x = leaf.leaf_val();\n"+
		"    if (x > 0) { return 42; }\n"+
		"    return 0;\n"+
		"}\n")
	write("main.fern", "import \"./mid\";\nfunction main(): i32 { return mid.mid_val(); }\n")
	return proj, filepath.Join(proj, "main.fern")
}

func pmCacheLines(stderr string) ([]string, []string) {
	hits, misses := []string{}, []string{}
	for _, ln := range strings.Split(stderr, "\n") {
		ln = strings.TrimSpace(ln)
		if m := strings.TrimPrefix(ln, "cache-hit "); m != ln {
			hits = append(hits, m)
		} else if m := strings.TrimPrefix(ln, "cache-miss "); m != ln {
			misses = append(misses, m)
		}
	}
	sort.Strings(hits)
	sort.Strings(misses)
	return hits, misses
}

func pmWantSets(t *testing.T, phase string, gotHits, gotMisses, wantHits, wantMisses []string) {
	t.Helper()
	if strings.Join(gotMisses, ",") != strings.Join(wantMisses, ",") {
		t.Fatalf("%s: re-emitted %v, want %v", phase, gotMisses, wantMisses)
	}
	if strings.Join(gotHits, ",") != strings.Join(wantHits, ",") {
		t.Fatalf("%s: served from cache %v, want %v", phase, gotHits, wantHits)
	}
}

func TestSelfHostPerModuleEmitAllObjectCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "drivers/asm_modload_run.fern", "driver")

	proj, entry := pmCacheTree(t, "@noinline pub function leaf_val(): i32 { return 40; }\n")
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	writeLeaf := func(body string) {
		if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte(body), 0o644); err != nil {
			t.Fatalf("write leaf.fern: %v", err)
		}
	}

	drive := func(args ...string) (string, string) {
		t.Helper()
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver %v: %v\nstderr:\n%s", args, err, errb.String())
		}
		return string(out), errb.String()
	}

	emitAll := func(tag string, useCache bool) (map[string]string, []string, []string) {
		outDir := filepath.Join(proj, "out_"+tag)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", outDir, err)
		}
		args := []string{"-per-module-emit-all", "-out-dir", outDir}
		if useCache {
			args = append(args, "-cache-dir", cacheDir)
		}
		_, errs := drive(args...)
		ents, err := os.ReadDir(outDir)
		if err != nil {
			t.Fatalf("read %s: %v", outDir, err)
		}
		units := map[string]string{}
		for _, e := range ents {
			b, err := os.ReadFile(filepath.Join(outDir, e.Name()))
			if err != nil {
				t.Fatalf("read unit %s: %v", e.Name(), err)
			}
			units[e.Name()] = string(b)
		}
		if len(units) < 3 {
			t.Fatalf("%s: expected >=3 units, got %d", tag, len(units))
		}
		hits, misses := pmCacheLines(errs)
		return units, hits, misses
	}

	assertClean := func(tag string, cached map[string]string) {
		t.Helper()
		clean, _, _ := emitAll(tag+"_clean", false)
		if len(clean) != len(cached) {
			t.Fatalf("%s: cached build has %d units, clean has %d", tag, len(cached), len(clean))
		}
		for name, want := range clean {
			if cached[name] != want {
				t.Fatalf("%s: unit %s differs from a clean build (%d vs %d bytes)",
					tag, name, len(cached[name]), len(want))
			}
		}
	}

	linkRun := func(tag string, units map[string]string) int {
		var names []string
		for n := range units {
			names = append(names, n)
		}
		sort.Strings(names)
		var objs []string
		for _, n := range names {
			p := filepath.Join(proj, tag+"_"+n)
			if err := os.WriteFile(p, []byte(units[n]), 0o644); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
			objs = append(objs, p)
		}
		bin := filepath.Join(proj, tag+"_bin")
		linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
		if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
			t.Fatalf("%s link: %v\n%s", tag, err, lout)
		}
		var rcmd *exec.Cmd
		if len(runner) == 0 {
			rcmd = exec.Command(bin)
		} else {
			rcmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		if err := rcmd.Run(); err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode()
			}
			t.Fatalf("%s run: %v", tag, err)
		}
		return 0
	}

	cold, hits, misses := emitAll("cold", true)
	pmWantSets(t, "cold", hits, misses, []string{}, []string{"__entry", "leaf", "mid"})
	assertClean("cold", cold)
	if code := linkRun("cold", cold); code != 42 {
		t.Fatalf("cold build exit %d, want 42", code)
	}

	warm, hits, misses := emitAll("warm", true)
	pmWantSets(t, "warm", hits, misses, []string{"__entry", "leaf", "mid"}, []string{})
	for name, want := range cold {
		if warm[name] != want {
			t.Fatalf("warm: unit %s changed with no source edit", name)
		}
	}

	writeLeaf("@noinline pub function leaf_val(): i32 { return 41; }\n")
	body, hits, misses := emitAll("body", true)
	pmWantSets(t, "body", hits, misses, []string{"__entry", "mid"}, []string{"leaf"})
	assertClean("body", body)
	if code := linkRun("body", body); code != 42 {
		t.Fatalf("body-edit build exit %d, want 42", code)
	}

	writeLeaf("@noinline pub function leaf_val(): i64 { return 40i64; }\n")
	sig, hits, misses := emitAll("sig", true)
	pmWantSets(t, "sig", hits, misses, []string{}, []string{"__entry", "leaf", "mid"})
	assertClean("sig", sig)
	if code := linkRun("sig", sig); code != 42 {
		t.Fatalf("sig-edit build exit %d, want 42", code)
	}
}

func TestSelfHostPerModuleCacheFollowsInlinedBodyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "drivers/asm_modload_run.fern", "driver")

	proj, entry := pmCacheTree(t, "pub function leaf_val(): i32 { return 40; }\n")
	if err := os.WriteFile(filepath.Join(proj, "mid.fern"), []byte("import \"./leaf\";\n@noinline pub function mid_val(): i32 { return leaf.leaf_val() + 2; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := func(tag string) (int, []string, []string) {
		t.Helper()
		outDir := filepath.Join(proj, "out_"+tag)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := runX86_64Bin(runner, driverBin, entry, "-per-module-emit-all", "-out-dir", outDir, "-cache-dir", cacheDir)
		var errb strings.Builder
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: emit-all: %v\n%s", tag, err, errb.String())
		}
		ents, err := os.ReadDir(outDir)
		if err != nil {
			t.Fatal(err)
		}
		var objs []string
		for _, e := range ents {
			objs = append(objs, filepath.Join(outDir, e.Name()))
		}
		sort.Strings(objs)
		bin := filepath.Join(proj, tag+"_bin")
		if lout, err := exec.Command(gcc, append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: link: %v\n%s", tag, err, lout)
		}
		run := runX86_64Bin(runner, bin)
		_ = run.Run()
		hits, misses := pmCacheLines(errb.String())
		return run.ProcessState.ExitCode(), hits, misses
	}

	if code, _, _ := build("cold"); code != 42 {
		t.Fatalf("cold build exit %d, want 42", code)
	}
	if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte("pub function leaf_val(): i32 { return 41; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, hits, misses := build("body")
	pmWantSets(t, "body", hits, misses, []string{"__entry"}, []string{"leaf", "mid"})
	if code != 43 {
		t.Fatalf("body-edit build exit %d, want 43", code)
	}
}

func TestSelfHostPerModuleSpawnedCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("spawned route execs the driver directly; not runnable under an emulator wrapper")
	}
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "drivers/asm_modload_run.fern", "driver")

	proj, entry := pmCacheTree(t, "@noinline pub function leaf_val(): i32 { return 40; }\n")
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	spawn := func(useCache bool) (string, string) {
		t.Helper()
		args := []string{entry, "-spawned"}
		if useCache {
			args = append(args, "-cache-dir", cacheDir)
		}
		cmd := exec.Command(driverBin, args...)
		var errb strings.Builder
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("spawned driver: %v\nstderr:\n%s", err, errb.String())
		}
		return string(out), errb.String()
	}

	cold, coldErr := spawn(true)
	_, coldMisses := pmCacheLines(coldErr)
	if len(coldMisses) == 0 {
		t.Fatalf("cold spawned build reported no cache-miss lines — -cache-dir never reached the batch children\nstderr:\n%s", coldErr)
	}

	warm, warmErr := spawn(true)
	warmHits, warmMisses := pmCacheLines(warmErr)
	if len(warmHits) == 0 {
		t.Fatalf("warm spawned build served nothing from cache\nstderr:\n%s", warmErr)
	}
	if len(warmMisses) != 0 {
		t.Fatalf("warm spawned build re-emitted %v with no source edit", warmMisses)
	}
	if warm != cold {
		t.Fatalf("spawned stream differs between cold and warm builds (%d vs %d bytes)", len(cold), len(warm))
	}

	clean, _ := spawn(false)
	if clean != warm {
		t.Fatalf("cache-served spawned stream differs from a clean spawned build (%d vs %d bytes)", len(warm), len(clean))
	}
}

func TestSelfHostPerModuleTransitiveInvalidationX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "drivers/asm_modload_run.fern", "driver")

	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("mid.fern", "import \"./leaf\";\n"+
		"@noinline pub function get(): leaf.Thing { return leaf.make_thing(); }\n")
	write("main.fern", "import \"./mid\";\n"+
		"function main(): i32 {\n"+
		"    let t = mid.get();\n"+
		"    return t.b + 1;\n"+
		"}\n")
	const leafBefore = "pub struct Thing { a: i32, b: i32 }\n" +
		"pub function make_thing(): Thing { return Thing { a: 1, b: 41 }; }\n"
	const leafAfter = "pub struct Thing { a: i32, z: i32, b: i32 }\n" +
		"pub function make_thing(): Thing { return Thing { a: 1, z: 0, b: 41 }; }\n"
	write("leaf.fern", leafBefore)
	entry := filepath.Join(proj, "main.fern")

	drive := func(args ...string) (string, string) {
		t.Helper()
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
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
	entryIdx := -1
	for i, ns := range nsOrder {
		if ns == "__entry" {
			entryIdx = i
		}
	}
	if entryIdx < 0 {
		t.Fatalf("no __entry module in %v", nsOrder)
	}

	var needArgs []string
	for _, ln := range strings.Split(mustStdout(drive("-per-module-needs")), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	buildAll := func() ([]string, []string) {
		var hits, misses []string
		for i := range nsOrder {
			_, errs := drive(append([]string{"-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir}, needArgs...)...)
			h, m := pmCacheLines(errs)
			hits = append(hits, h...)
			misses = append(misses, m...)
		}
		sort.Strings(hits)
		sort.Strings(misses)
		return hits, misses
	}

	buildAll()
	entryBefore := mustStdout(drive(append([]string{"-per-module-emit", strconv.Itoa(entryIdx)}, needArgs...)...))

	write("leaf.fern", leafAfter)
	entryAfter := mustStdout(drive(append([]string{"-per-module-emit", strconv.Itoa(entryIdx)}, needArgs...)...))
	if entryBefore == entryAfter {
		t.Fatal("entry unit is identical across the field-layout change, so this test could not " +
			"distinguish correct invalidation from reuse — pick a shape whose codegen moves")
	}

	hits, misses := buildAll()
	joined := strings.Join(misses, ",")
	if !strings.Contains(joined, "__entry") {
		t.Fatalf("__entry was served from cache after a transitively-imported struct changed layout "+
			"— that is the stale unit above (hits=%v misses=%v)", hits, misses)
	}
}
