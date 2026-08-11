package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestSelfHostCapsRules exercises the self-host's package-capability boundary
// (examples/self_host/caps.fern, #6634) — the port of native's internal/caps,
// and the second of the two independent capability systems.
//
// The driver asserts each rule in BOTH directions: a package that must be
// reported and one that must not, a grant that must hold and one that must
// not. The permissive half carries the weight — a grant check that fires on a
// package using only what it was granted refuses code that is correct by the
// manifest's own terms.
//
// Exit 0 means every assertion held. A non-zero code identifies the case, so a
// regression names itself without a stdout diff.
func TestSelfHostCapsRules(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("caps_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "caps_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "caps_run.fern", "caps_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("caps_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("caps_run exit code = %d, want 0 — that code is the failing assertion's id in caps_run.fern", code)
	}
	if want := "caps: every package-capability rule agrees"; !strings.Contains(string(out), want) {
		t.Errorf("caps_run stdout = %q, want it to contain %q", out, want)
	}
}

// writeCapsProject lays out the two-package tree both compilers are run over:
// an `app` root package depending on a path dependency `helper` whose lib
// reaches for the filesystem, with `helper`'s dependency entry carrying
// whatever grant the case is testing.
func writeCapsProject(t *testing.T, dir, depEntry, helperBody string) string {
	t.Helper()
	files := map[string]string{
		"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\n" + depEntry + "\n",
		"app/main.fern":    "import \"helper\";\nfunction main(): i32 {\n  var r: i32 = helper.save(\"x\");\n  return r;\n}\n",
		"helper/fern.toml": "[package]\nname = \"helper\"\n",
		"helper/lib.fern":  helperBody,
	}
	for rel, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "app", "main.fern")
}

// capsDiagnostics reduces a compiler's output to its capability diagnostics,
// one per line as `severity package capability builtin`.
//
// The example CHAIN is deliberately dropped. Its intermediate frames are the
// bundle's mangled function names, and the two compilers mangle a dependency
// differently: native by the dependency's lib FILE (`lib__save`), the
// self-host by the name the import used (`helper__save`). Every part that
// decides whether a program is refused — the severity, the package, the
// capability, and the builtin that carries it — is compared exactly.
func capsDiagnostics(out string) []string {
	re := regexp.MustCompile(`(error\[E070\]|warning): package "([^"]+)" reaches '([^']+)' \(([^)]+)\)`)
	var got []string
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		severity := "error"
		if m[1] == "warning" {
			severity = "warning"
		}
		got = append(got, severity+" "+m[2]+" "+m[3]+" "+m[4])
	}
	sort.Strings(got)
	return got
}

// TestSelfHostPackageCapabilityDifferentialX86_64 is the parity gate: for a
// program whose dependency reaches past its grant, native and the self-host
// must agree on whether it compiles.
//
// This is the divergence shape that blocks "the self-host is the only
// compiler" — not a message worded differently, but a project native refuses
// and the self-host builds. Before #6634 the self-host had no package
// capability layer at all (and could not even resolve a manifest dependency),
// so every case below built clean there.
//
// The COMPILE path is what the two are compared on, not `-check`: the
// self-host's partial checker cannot yet type a cross-module call at all
// (#4346), so its `-check` exit code is dominated by that hint for any
// multi-module program, while the compile path answers exactly the question
// this gate is about — does a binary come out.
func TestSelfHostPackageCapabilityDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("package capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const reachesFS = "pub function save(s: string): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", s);\n  return 0;\n}\n"
	const reachesNothing = "pub function save(s: string): i32 {\n  return s.len();\n}\n"

	for _, c := range []struct {
		name      string
		depEntry  string
		helper    string
		wantBuild bool
	}{
		// Granted exactly what it uses: both compilers build it. This is the
		// half that matters most — a false positive here refuses a project
		// whose manifest already says yes.
		{"granted", "helper = { path = \"../helper\", capabilities = [\"fs\"] }", reachesFS, true},
		// Granted something else: refused by both, E070.
		{"outside-grant", "helper = { path = \"../helper\", capabilities = [\"net\"] }", reachesFS, false},
		// An empty grant denies everything, which is a different answer from
		// no key at all.
		{"empty-grant", "helper = { path = \"../helper\", capabilities = [] }", reachesFS, false},
		// No `capabilities` key: warn-and-allow, the migration default for
		// manifests that predate the key. It must still BUILD.
		{"ungoverned", "helper = { path = \"../helper\" }", reachesFS, true},
		// A dependency reaching for nothing is clean under any grant.
		{"reaches-nothing", "helper = { path = \"../helper\", capabilities = [] }", reachesNothing, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			proj := t.TempDir()
			entry := writeCapsProject(t, proj, c.depEntry, c.helper)

			nativeCmd := exec.Command(nativeBin, "-o", filepath.Join(proj, "native.bin"), entry)
			nativeOut, _ := nativeCmd.CombinedOutput()
			nativeBuilt := nativeCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-o", filepath.Join(proj, "selfhost.bin"), entry, stdlib)
			shOut, _ := shCmd.CombinedOutput()
			shBuilt := shCmd.ProcessState.ExitCode() == 0

			if nativeBuilt != c.wantBuild {
				t.Fatalf("native built = %v, want %v\n%s", nativeBuilt, c.wantBuild, nativeOut)
			}
			if shBuilt != nativeBuilt {
				t.Errorf("native built = %v, self-host built = %v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeBuilt, shBuilt, nativeOut, shOut)
			}
			want := capsDiagnostics(string(nativeOut))
			got := capsDiagnostics(string(shOut))
			if strings.Join(want, "; ") != strings.Join(got, "; ") {
				t.Errorf("capability diagnostics differ:\nnative    %v\nself-host %v\n--- native ---\n%s\n--- self-host ---\n%s",
					want, got, nativeOut, shOut)
			}
		})
	}
}

// TestSelfHostCapabilitiesReportAgreesWithNative pins report mode
// (`-capabilities`, phase 1 of docs/PACKAGE-CAPABILITIES-BRIEF.md): the same
// packages, each reaching the same capabilities, from both compilers.
//
// The report is what a user reads before writing a grant, so a package missing
// from one compiler's report is a grant nobody knows to write. Only the
// example chain is normalised away (see capsDiagnostics on why the mangling
// differs); the package names and their capability sets are compared exactly.
func TestSelfHostCapabilitiesReportAgreesWithNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capabilities report differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	proj := t.TempDir()
	entry := writeCapsProject(t, proj, "helper = { path = \"../helper\" }",
		"pub function save(s: string): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", s);\n  tcp_connect(0, 80);\n  return 0;\n}\n")

	nativeOut, err := exec.Command(nativeBin, "-capabilities", entry).CombinedOutput()
	if err != nil {
		t.Fatalf("native -capabilities: %v\n%s", err, nativeOut)
	}
	shOut, err := exec.Command(driverBin, "-capabilities", entry, stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("self-host -capabilities: %v\n%s", err, shOut)
	}
	if want, got := reportRows(string(nativeOut)), reportRows(string(shOut)); want != got {
		t.Errorf("report rows differ:\nnative    %q\nself-host %q\n--- native ---\n%s\n--- self-host ---\n%s",
			want, got, nativeOut, shOut)
	}
	// The report must actually have found something, or the comparison above
	// passes on two empty strings.
	if !strings.Contains(reportRows(string(nativeOut)), "helper fs,net") {
		t.Errorf("native report did not attribute fs,net to helper: %q", nativeOut)
	}
}

// reportRows strips `-capabilities` output down to `package caps` per line,
// dropping the example chain.
func reportRows(out string) string {
	var rows []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		rows = append(rows, fields[0]+" "+fields[1])
	}
	sort.Strings(rows)
	return strings.Join(rows, "; ")
}
