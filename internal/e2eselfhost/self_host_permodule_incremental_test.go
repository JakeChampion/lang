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

// TestSelfHostPerModuleIncrementalCodegenX86_64 exercises the per-module
// incremental-codegen manifest (#3451 step 6 / #3458): the driver's
// `-per-module-manifest` mode prints, per module,
//
//	<ns>|<src_hash>|<sig_hash>|<dep1,dep2,...>
//
// which is everything a build orchestrator needs to decide — correctly — which
// modules to re-emit on a rebuild and which to reuse from cache. A module's
// cache key is its own src_hash folded with the SIG hash of every module it
// depends on — the transitive closure the dependency column reports, so an
// orchestrator folding that column verbatim gets the driver's own key. That
// split is the whole point:
//
//   - src_hash makes a body-only edit re-emit that module (its own codegen
//     changed) while leaving dependents alone (a dependency's *signature* is
//     unchanged, so their call-site codegen is unchanged).
//   - sig_hash makes a *signature* edit to a dependency invalidate the modules
//     that depend on it, even though their own source is byte-for-byte
//     unchanged — because per-module emit tags cross-module calls by the
//     callee's signature (the whole-program side-tables, #3454). Keying on
//     source alone would silently ship stale codegen here; this test's
//     scenario B is the guard the issue asks for.
//
// The tree: main → mid → leaf. `mid_val` reads leaf's return value into an
// inferred local, so its emitted code depends on leaf's *return type* while its
// own source never mentions it — the exact shape that makes a source-only cache
// wrong.
func TestSelfHostPerModuleIncrementalCodegenX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "driver")

	// The program tree lives in its own dir (the driver resolves imports
	// relative to the entry). leaf_val's return type flows — via an inferred
	// local — into mid_val's codegen without mid ever naming it.
	proj := t.TempDir()
	writeLeaf := func(body string) {
		if err := os.WriteFile(filepath.Join(proj, "leaf.fern"), []byte(body), 0o644); err != nil {
			t.Fatalf("write leaf.fern: %v", err)
		}
	}
	const leafI32 = "pub function leaf_val(): i32 { return 40; }\n"
	const leafI32Body = "pub function leaf_val(): i32 { return 41; }\n" // body-only change, signature identical
	const leafI64 = "pub function leaf_val(): i64 { return 40i64; }\n"  // signature change
	writeLeaf(leafI32)
	if err := os.WriteFile(filepath.Join(proj, "mid.fern"), []byte(
		"import \"./leaf\";\n"+
			"pub function mid_val(): i32 {\n"+
			"    var x = leaf.leaf_val();\n"+
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

	drive := func(args ...string) string {
		t.Helper()
		full := append([]string{entry}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, full...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), full...)...)
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("driver %v: %v", args, err)
		}
		return string(out)
	}

	// The runtime-need union (a static over-approximation, #3456) is invariant
	// across these edits, so gather it once for every link below.
	var needArgs []string
	for _, ln := range strings.Split(drive("-per-module-needs"), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			needArgs = append(needArgs, "-extra-need", s)
		}
	}

	type modInfo struct {
		ns, srcHash, sigHash string
		imports              []string
	}
	// manifest returns the modules IN EMIT-INDEX ORDER (line i ↔
	// `-per-module-emit i`) plus a by-ns lookup.
	manifest := func() ([]modInfo, map[string]modInfo) {
		var order []modInfo
		byNS := map[string]modInfo{}
		for _, ln := range strings.Split(drive("-per-module-manifest"), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			f := strings.Split(ln, "|")
			if len(f) != 4 {
				t.Fatalf("malformed manifest line %q", ln)
			}
			m := modInfo{ns: f[0], srcHash: f[1], sigHash: f[2]}
			if f[3] != "" {
				m.imports = strings.Split(f[3], ",")
			}
			order = append(order, m)
			byNS[m.ns] = m
		}
		if len(order) == 0 {
			t.Fatal("empty manifest")
		}
		return order, byNS
	}

	// cacheKey folds a module's own src_hash with the sig_hash of each module it
	// depends on (sorted, so it's order-independent). This is the invalidation
	// contract: change a dependency's signature and every dependent's key moves.
	//
	// The manifest's dependency column is the TRANSITIVE closure, not the direct
	// edges, so folding it verbatim reproduces the driver's own key. That is the
	// point of reporting the closure: an orchestrator that walked direct edges
	// would compute a weaker key than the driver and reuse a unit the driver
	// would have re-emitted.
	cacheKey := func(m modInfo, byNS map[string]modInfo) string {
		parts := []string{m.srcHash}
		var deps []string
		for _, imp := range m.imports {
			dep, ok := byNS[imp]
			if !ok {
				continue // not a project module (intrinsic / builtin) — no sig edge
			}
			deps = append(deps, dep.sigHash)
		}
		sort.Strings(deps)
		parts = append(parts, deps...)
		return strings.Join(parts, "#")
	}

	keysOf := func(order []modInfo, byNS map[string]modInfo) map[string]string {
		keys := map[string]string{}
		for _, m := range order {
			keys[m.ns] = cacheKey(m, byNS)
		}
		return keys
	}

	changedNS := func(a, b map[string]string) []string {
		var out []string
		for ns, kb := range b {
			if a[ns] != kb {
				out = append(out, ns)
			}
		}
		sort.Strings(out)
		return out
	}

	emitUnit := func(i int) string {
		return drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
	}

	// linkAndRun assembles+links the per-ns unit asm and runs the binary,
	// returning its exit code. It also returns the concatenated unit bytes so a
	// caller can assert an incremental build is byte-identical to a clean one.
	linkAndRun := func(tag string, units map[string]string, order []modInfo) (int, string) {
		var objs []string
		var concat strings.Builder
		for i, m := range order {
			asm := units[m.ns]
			if asm == "" {
				t.Fatalf("%s: missing unit asm for %q", tag, m.ns)
			}
			concat.WriteString(asm)
			p := filepath.Join(proj, tag+"_u"+strconv.Itoa(i)+".s")
			if err := os.WriteFile(p, []byte(asm), 0o644); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
			objs = append(objs, p)
		}
		bin := filepath.Join(proj, tag+"_bin")
		linkArgs := append([]string{"-static", "-nostdlib", "-no-pie"}, append(objs, "-o", bin)...)
		if lout, err := exec.Command(gcc, linkArgs...).CombinedOutput(); err != nil {
			t.Fatalf("%s: link failed: %v\n%s", tag, err, lout)
		}
		var rcmd *exec.Cmd
		if len(runner) == 0 {
			rcmd = exec.Command(bin)
		} else {
			rcmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = rcmd.Run()
		return rcmd.ProcessState.ExitCode(), concat.String()
	}

	// --- Clean build v1 (leaf: i32, return 40) ---
	order1, byNS1 := manifest()
	keys1 := keysOf(order1, byNS1)
	// cache maps a cache key -> emitted unit asm, exactly as an object cache would.
	cache := map[string]string{}
	units1 := map[string]string{}
	for i, m := range order1 {
		asm := emitUnit(i)
		if asm == "" {
			t.Fatalf("v1: module %q emitted 0 bytes", m.ns)
		}
		units1[m.ns] = asm
		cache[keys1[m.ns]] = asm
	}
	if code, _ := linkAndRun("v1", units1, order1); code != 42 {
		t.Fatalf("v1 clean build ran with exit %d, want 42", code)
	}

	// rebuild does an incremental build against `cache`: reuse the cached unit
	// when a module's key is already present, re-emit otherwise. It returns the
	// set of modules that were re-emitted (the invalidation set).
	rebuild := func(tag string, order []modInfo, keys map[string]string) (map[string]string, []string) {
		units := map[string]string{}
		var reemitted []string
		for i, m := range order {
			k := keys[m.ns]
			if hit, ok := cache[k]; ok {
				units[m.ns] = hit
				continue
			}
			asm := emitUnit(i)
			if asm == "" {
				t.Fatalf("%s: module %q emitted 0 bytes", tag, m.ns)
			}
			units[m.ns] = asm
			cache[k] = asm
			reemitted = append(reemitted, m.ns)
		}
		sort.Strings(reemitted)
		return units, reemitted
	}

	eq := func(a, b []string) bool {
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

	// ================= Scenario A: body-only edit to leaf =================
	// leaf's signature is unchanged, only its body. Exactly one key must move
	// (leaf's own); mid and __entry must be reused untouched.
	writeLeaf(leafI32Body)
	orderA, byNSA := manifest()
	keysA := keysOf(orderA, byNSA)

	if got, want := byNSA["leaf"].sigHash, byNS1["leaf"].sigHash; got != want {
		t.Errorf("A: leaf sig_hash moved on a body-only edit (%q -> %q)", want, got)
	}
	if got := changedNS(keys1, keysA); !eq(got, []string{"leaf"}) {
		t.Fatalf("A: body edit changed cache keys %v, want exactly [leaf]", got)
	}
	unitsA, reA := rebuild("A", orderA, keysA)
	if !eq(reA, []string{"leaf"}) {
		t.Fatalf("A: incremental rebuild re-emitted %v, want exactly [leaf]", reA)
	}
	// mid and __entry must be the byte-identical cached v1 units (reuse, not
	// recompute).
	for _, ns := range []string{"mid", "__entry"} {
		if unitsA[ns] != units1[ns] {
			t.Errorf("A: %q was not reused byte-identically from cache", ns)
		}
	}
	// A clean rebuild of the same edited tree must produce byte-identical units
	// (and hence a byte-identical binary) to the incremental one.
	cleanA := map[string]string{}
	for i, m := range orderA {
		cleanA[m.ns] = emitUnit(i)
	}
	codeAinc, concatAinc := linkAndRun("Ainc", unitsA, orderA)
	codeAcln, concatAcln := linkAndRun("Acln", cleanA, orderA)
	if codeAinc != 42 || codeAcln != 42 {
		t.Fatalf("A: exit codes inc=%d clean=%d, want 42/42", codeAinc, codeAcln)
	}
	if concatAinc != concatAcln {
		t.Fatalf("A: incremental build not byte-identical to clean build")
	}

	// ================= Scenario B: signature edit to leaf =================
	// leaf's return type changes i32 -> i64. leaf's key moves (source + sig),
	// and mid's key MUST move too — because mid calls into leaf — even though
	// mid's own source is byte-for-byte unchanged. This is the stale-codegen
	// guard: a source-only cache would reuse the old mid unit and ship wrong code.
	//
	// __entry's key moves as well. It reaches leaf only transitively, and its
	// codegen does not actually depend on leaf's return type here — but the key
	// folds the whole dependency closure, because a unit lowers under the
	// whole-program view and can reference declarations it never imported
	// (TestSelfHostPerModuleTransitiveInvalidationX86_64 has the shape where that
	// is a miscompile). The closure is a sound over-approximation, so a chain like
	// this one re-emits a unit it could in principle have kept.
	writeLeaf(leafI64)
	orderB, byNSB := manifest()
	keysB := keysOf(orderB, byNSB)

	if got, want := byNSB["mid"].srcHash, byNSA["mid"].srcHash; got != want {
		t.Fatalf("B: mid src_hash changed (%q -> %q) — the test can't isolate signature-driven invalidation", want, got)
	}
	if byNSB["leaf"].sigHash == byNSA["leaf"].sigHash {
		t.Fatalf("B: leaf sig_hash did NOT change on a return-type edit")
	}
	if got := changedNS(keysA, keysB); !eq(got, []string{"__entry", "leaf", "mid"}) {
		t.Fatalf("B: signature edit changed cache keys %v, want exactly [__entry leaf mid]", got)
	}

	// Direct proof this is not over-caution: mid's freshly-emitted unit really
	// does differ from the cached (v1/A) mid unit, so a source-only cache would
	// have shipped stale codegen for mid.
	var midIdxB int
	for i, m := range orderB {
		if m.ns == "mid" {
			midIdxB = i
		}
	}
	freshMidB := emitUnit(midIdxB)
	if freshMidB == units1["mid"] {
		t.Fatalf("B: mid unit unchanged across leaf's signature change — guard is vacuous")
	}

	unitsB, reB := rebuild("B", orderB, keysB)
	if !eq(reB, []string{"__entry", "leaf", "mid"}) {
		t.Fatalf("B: incremental rebuild re-emitted %v, want exactly [__entry leaf mid]", reB)
	}
	cleanB := map[string]string{}
	for i, m := range orderB {
		cleanB[m.ns] = emitUnit(i)
	}
	codeBinc, concatBinc := linkAndRun("Binc", unitsB, orderB)
	codeBcln, concatBcln := linkAndRun("Bcln", cleanB, orderB)
	if codeBinc != 42 || codeBcln != 42 {
		t.Fatalf("B: exit codes inc=%d clean=%d, want 42/42", codeBinc, codeBcln)
	}
	if concatBinc != concatBcln {
		t.Fatalf("B: incremental build not byte-identical to clean build")
	}
}

// TestSelfHostPerModuleObjectCacheX86_64 exercises the driver's OWN on-disk
// object cache (#3451 step 6 / #3458): `-per-module-emit N -cache-dir DIR`.
// Where TestSelfHostPerModuleIncrementalCodegen models the cache in the test
// harness off the manifest, this proves the driver itself computes the cache
// key, serves a hit from disk (skipping codegen), and populates on a miss —
// reporting hit/miss per module on stderr while stdout stays exactly the unit
// asm. The invalidation contract is identical: a body-only edit re-emits just
// that module; a dependency's signature change re-emits that module and
// everything that depends on it, with the rest served byte-for-byte from cache.
func TestSelfHostPerModuleObjectCacheX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	shDir := writeSelfHostModloadProject(t)
	driverBin := buildSelfHostBin(t, gcc, shDir, "asm_modload_run.fern", "driver")

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
	writeLeaf("pub function leaf_val(): i32 { return 40; }\n")
	if err := os.WriteFile(filepath.Join(proj, "mid.fern"), []byte(
		"import \"./leaf\";\n"+
			"pub function mid_val(): i32 {\n"+
			"    var x = leaf.leaf_val();\n"+
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

	// drive returns (stdout, stderr); stderr carries the cache-hit/miss lines.
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

	// Module namespaces in emit-index order (from the manifest).
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

	// buildWithCache emits every module through the cache, returning the per-ns
	// unit asm and the sets of cache hits / misses observed.
	buildWithCache := func() (map[string]string, []string, []string) {
		units := map[string]string{}
		var hits, misses []string
		for i, ns := range nsOrder {
			args := append([]string{"-per-module-emit", strconv.Itoa(i), "-cache-dir", cacheDir}, needArgs...)
			out, errs := drive(args...)
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

	// cleanUnits emits every module fresh (no cache) — the reference a cached
	// build must match byte-for-byte.
	cleanUnits := func() map[string]string {
		units := map[string]string{}
		for i, ns := range nsOrder {
			out, _ := drive(append([]string{"-per-module-emit", strconv.Itoa(i)}, needArgs...)...)
			units[ns] = out
		}
		return units
	}

	linkRun := func(tag string, units map[string]string) int {
		var objs []string
		for i, ns := range nsOrder {
			p := filepath.Join(proj, tag+"_u"+strconv.Itoa(i)+".s")
			if err := os.WriteFile(p, []byte(units[ns]), 0o644); err != nil {
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
		_ = rcmd.Run()
		return rcmd.ProcessState.ExitCode()
	}

	assertUnitsMatch := func(tag string, got map[string]string) {
		clean := cleanUnits()
		for _, ns := range nsOrder {
			if got[ns] != clean[ns] {
				t.Errorf("%s: unit %q not byte-identical to a clean emit", tag, ns)
			}
		}
	}

	// eq reports whether sorted `a` equals the set `want`. It copies `want`
	// before sorting: a bare sort.Strings(want) would mutate the caller's slice
	// in place, because a variadic argument passed as `nsOrder...` aliases
	// nsOrder — reordering it under the later cleanUnits loop.
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

	// --- Cold build: every module a miss, cache populated. ---
	units, hits, misses := buildWithCache()
	if len(hits) != 0 || !eq(misses, nsOrder...) {
		t.Fatalf("cold build: hits=%v misses=%v, want no hits and all-miss", hits, misses)
	}
	if code := linkRun("cold", units); code != 42 {
		t.Fatalf("cold build exit %d, want 42", code)
	}
	assertUnitsMatch("cold", units)

	// --- Warm build, no edits: every module a hit, bytes unchanged. ---
	units2, hits2, misses2 := buildWithCache()
	if !eq(hits2, nsOrder...) || len(misses2) != 0 {
		t.Fatalf("warm build: hits=%v misses=%v, want all-hit and no miss", hits2, misses2)
	}
	for _, ns := range nsOrder {
		if units2[ns] != units[ns] {
			t.Errorf("warm build: unit %q changed despite no edit", ns)
		}
	}
	if code := linkRun("warm", units2); code != 42 {
		t.Fatalf("warm build exit %d, want 42", code)
	}

	// --- Body-only edit to leaf: only leaf re-emits; mid/__entry served. ---
	writeLeaf("pub function leaf_val(): i32 { return 41; }\n")
	unitsA, hitsA, missesA := buildWithCache()
	if !eq(missesA, "leaf") {
		t.Fatalf("body edit: misses=%v, want exactly [leaf]", missesA)
	}
	if !eq(hitsA, "mid", "__entry") {
		t.Fatalf("body edit: hits=%v, want exactly [mid __entry]", hitsA)
	}
	if code := linkRun("bodyedit", unitsA); code != 42 {
		t.Fatalf("body-edit build exit %d, want 42", code)
	}
	assertUnitsMatch("bodyedit", unitsA)

	// --- Signature edit to leaf (i32 -> i64): everything in leaf's dependency
	// closure re-emits. mid's call-site codegen depends on leaf's return type
	// directly; __entry reaches it transitively, and the key folds the whole
	// closure because a unit lowers under the whole-program view and can
	// reference declarations it never imported. This is the guard: a source-only
	// cache would serve a stale mid unit. ---
	writeLeaf("pub function leaf_val(): i64 { return 41i64; }\n")
	unitsB, hitsB, missesB := buildWithCache()
	if !eq(missesB, "leaf", "mid", "__entry") {
		t.Fatalf("sig edit: misses=%v, want exactly [leaf mid __entry]", missesB)
	}
	if len(hitsB) != 0 {
		t.Fatalf("sig edit: hits=%v, want nothing served", hitsB)
	}
	if code := linkRun("sigedit", unitsB); code != 42 {
		t.Fatalf("sig-edit build exit %d, want 42", code)
	}
	assertUnitsMatch("sigedit", unitsB)
}

// mustStdout is a tiny adapter for the (stdout, stderr) drive helper when only
// stdout matters.
func mustStdout(stdout, _ string) string { return stdout }
