package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The per-module object cache used to be reachable only through the explicit
// single-unit mode (`-per-module-emit N -cache-dir DIR`), which no ordinary
// build runs: a program over the single-process ceiling goes through
// emit_per_module_spawned, which execs `-per-module-emit-all` children, and
// neither of those consulted a cache. So the mechanism was complete and the
// default build was still a full re-emit every time (#5330, remainder 1 of
// #3458). The tests here drive the two default-flow modes.

// pmCacheTree writes the leaf → mid → main chain the cache tests share and
// returns the project dir and entry path. `leaf` is the module edits are made
// to; mid calls into it, main calls mid.
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
		"    var x = leaf.leaf_val();\n"+
		"    if (x > 0) { return 42; }\n"+
		"    return 0;\n"+
		"}\n")
	write("main.fern", "import \"./mid\";\nfunction main(): i32 { return mid.mid_val(); }\n")
	return proj, filepath.Join(proj, "main.fern")
}

// pmCacheLines splits a driver's stderr into the sorted sets of namespaces it
// reported as served from cache and as re-emitted.
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

// TestSelfHostPerModuleEmitAllObjectCacheX86_64 drives the object cache through
// `-per-module-emit-all`, the mode every batch child of the default build flow
// runs in. It asserts the whole invalidation contract on the unit FILES that
// mode writes: cold misses everything, a warm rebuild serves everything, a
// body-only edit re-emits just that module, and a signature edit re-emits it and
// its dependents — with every phase byte-identical to a clean no-cache build, so
// a served unit is never merely plausible.
func TestSelfHostPerModuleEmitAllObjectCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "driver")

	proj, entry := pmCacheTree(t, "pub function leaf_val(): i32 { return 40; }\n")
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

	// emitAll runs one emit-all into a fresh output dir and returns the unit
	// files by name plus the cache hit / miss sets. With cacheDir empty it is the
	// clean reference build.
	emitAll := func(tag string, useCache bool) (map[string]string, []string, []string) {
		outDir := filepath.Join(proj, "out_"+tag)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", outDir, err)
		}
		args := []string{"-per-module-emit-all", "-out-dir", outDir, "-assume-eligible"}
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

	// assertClean re-emits the same sources with no cache at all and requires the
	// cached build to match it byte-for-byte. This is what separates "the cache
	// served something" from "the cache served the right thing".
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

	// linkRun assembles + links the emitted units and returns the exit code. The
	// entry unit carries the full runtime-need roots in emit-all mode, so no
	// -extra-need threading is needed here.
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

	// Phase 1 — cold: nothing is cached, so every unit is emitted.
	cold, hits, misses := emitAll("cold", true)
	pmWantSets(t, "cold", hits, misses, []string{}, []string{"__entry", "leaf", "mid"})
	assertClean("cold", cold)
	if code := linkRun("cold", cold); code != 42 {
		t.Fatalf("cold build exit %d, want 42", code)
	}

	// Phase 2 — warm: no source moved, so codegen runs for nothing at all.
	warm, hits, misses := emitAll("warm", true)
	pmWantSets(t, "warm", hits, misses, []string{"__entry", "leaf", "mid"}, []string{})
	for name, want := range cold {
		if warm[name] != want {
			t.Fatalf("warm: unit %s changed with no source edit", name)
		}
	}

	// Phase 3 — body-only edit: leaf's signature is untouched, so nothing that
	// merely calls into it can have changed. Only leaf re-emits.
	writeLeaf("pub function leaf_val(): i32 { return 41; }\n")
	body, hits, misses := emitAll("body", true)
	pmWantSets(t, "body", hits, misses, []string{"__entry", "mid"}, []string{"leaf"})
	assertClean("body", body)
	if code := linkRun("body", body); code != 42 {
		t.Fatalf("body-edit build exit %d, want 42", code)
	}

	// Phase 4 — signature edit: leaf's return type moves. mid calls it directly;
	// main reaches it transitively, and the key folds the whole import closure
	// because a unit lowers under the whole-program view (see
	// TestSelfHostPerModuleTransitiveInvalidationX86_64 for the shape that makes
	// direct-edge keying unsound). So the closure over leaf re-emits, which here
	// is everything.
	writeLeaf("pub function leaf_val(): i64 { return 40i64; }\n")
	sig, hits, misses := emitAll("sig", true)
	pmWantSets(t, "sig", hits, misses, []string{}, []string{"__entry", "leaf", "mid"})
	assertClean("sig", sig)
	if code := linkRun("sig", sig); code != 42 {
		t.Fatalf("sig-edit build exit %d, want 42", code)
	}
}

// TestSelfHostPerModuleSpawnedCacheX86_64 drives the DEFAULT build route end to
// end: `-spawned` forces emit_per_module_spawned, which forks a child per batch.
// The cache only reaches those children if the driver forwards -cache-dir into
// the exec argv, so this is the test that would fail if the flag stopped being
// threaded — the emit-all test above drives a child's mode directly and cannot
// see that.
//
// Skipped when the host needs an emulator to run the driver: the driver execs
// ITSELF by path, and a forked child would not be re-wrapped in the runner.
func TestSelfHostPerModuleSpawnedCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("spawned route execs the driver directly; not runnable under an emulator wrapper")
	}
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "driver")

	proj, entry := pmCacheTree(t, "pub function leaf_val(): i32 { return 40; }\n")
	cacheDir := filepath.Join(proj, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}

	spawn := func(useCache bool) (string, string) {
		t.Helper()
		args := []string{entry, "-spawned", "-assume-eligible"}
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

	// And the cached stream must equal what the same route produces with no
	// cache at all.
	clean, _ := spawn(false)
	if clean != warm {
		t.Fatalf("cache-served spawned stream differs from a clean spawned build (%d vs %d bytes)", len(warm), len(clean))
	}
}

// TestSelfHostPerModuleTransitiveInvalidationX86_64 pins the dependency rule the
// cache key folds signatures over.
//
// The key used to fold the signatures of a module's DIRECT imports only. That is
// unsound, because a unit is lowered under the whole-program struct and function
// view: `main` here imports `mid` and nothing else, yet reads a field of a
// struct `leaf` declares, so main's codegen embeds a field OFFSET that leaf owns.
// Inserting a field into that struct leaves mid's own signature untouched
// (`get(): Thing` is unchanged), so under the direct-edge rule main's key did not
// move and the build served main's PREVIOUS unit — reading the field at its old
// offset.
//
// The test is written so it cannot pass vacuously: it asserts the freshly
// emitted entry unit actually DIFFERS across the edit, which is what makes
// serving the cached one a miscompile rather than a harmless reuse.
func TestSelfHostPerModuleTransitiveInvalidationX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "driver")

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
	// main imports mid only; the struct it reads a field of is declared in leaf.
	write("mid.fern", "import \"./leaf\";\n"+
		"pub function get(): leaf.Thing { return leaf.make_thing(); }\n")
	write("main.fern", "import \"./mid\";\n"+
		"function main(): i32 {\n"+
		"    var t = mid.get();\n"+
		"    return t.b + 1;\n"+
		"}\n")
	const leafBefore = "pub struct Thing { a: i32, b: i32 }\n" +
		"pub function make_thing(): Thing { return Thing { a: 1, b: 41 }; }\n"
	// A field inserted BEFORE b, so every reader of `.b` must be re-emitted.
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

	// Cold build populates the cache, and records what the entry unit looks like
	// under leaf's ORIGINAL field layout.
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
