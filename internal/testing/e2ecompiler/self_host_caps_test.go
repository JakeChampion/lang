package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestSelfHostCapsRules(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("caps_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/caps_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/caps_run.fern", "caps_run")

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

func TestSelfHostCapsPathIdentityWithoutProcessDirectory(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := copySelfHostTree(t)
	entry := filepath.Join(dir, "caps_path_identity.fern")
	src := `import "./pkggrants";
function main(): i32 {
  if (pkggrants.canonical_dir("/one/../two") != "/two") { return 1; }
  let relative = pkggrants.canonical_dir("./one/../two");
  if (target_os() == "wasi") {
    if (relative != "two") { return 2; }
  } else {
    match (getcwd()) {
      Ok(cwd) => { if (relative != cwd + "/two") { return 3; } },
      Err(_) => { return 4; }
    }
  }
  return 0;
}`
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("native", func(t *testing.T) {
		if out, err := runX86_64Bin(cli.runner, cli.x86Binary(t, entry)).CombinedOutput(); err != nil {
			t.Fatalf("path identity: %v\n%s", err, out)
		}
	})
	t.Run("wasi", func(t *testing.T) {
		wat, err := os.ReadFile(cli.emit(t, entry, "wasm32-wasi"))
		if err != nil {
			t.Fatal(err)
		}
		if code, out := runWasm(t, string(wat)); code != 0 {
			t.Fatalf("path identity: exit %d\n%s", code, out)
		}
	})
}

// appExtra holds more declarations for the app package's main.fern.
func writeCapsProject(t *testing.T, dir, depEntry, helperBody string, appExtra ...string) string {
	t.Helper()
	files := map[string]string{
		"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\n" + depEntry + "\n",
		"app/main.fern":    "import \"helper\";\nfunction main(): i32 {\n  let r: i32 = helper.save(\"x\");\n  return r;\n}\n" + strings.Join(appExtra, ""),
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

func capsDiagnostics(out string) []string {
	re := regexp.MustCompile(`(?:(\S+\.fern):(\d+):(\d+): )?(error\[E070\]|warning\[W002\]): package "([^"]+)" reaches '([^']+)' \(([^)]+)\)[^:]*: ([^;]+);`)
	var got []string
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		severity := "error"
		if m[4] == "warning[W002]" {
			severity = "warning"
		}
		at := "-"
		if m[1] != "" {
			at = filepath.Base(m[1]) + ":" + m[2] + ":" + m[3]
		}
		got = append(got, strings.Join([]string{severity, m[5], m[6], m[7], m[8], at}, " "))
	}
	sort.Strings(got)
	return got
}

func TestSelfHostPackageCapabilityDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("package capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	oracleBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const reachesFS = "pub function save(s: string): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", s);\n  return 0;\n}\n"
	const reachesNothing = "pub function save(s: string): i32 {\n  return s.len();\n}\n"
	// A parameter that shares its name with an app function is not a reference to it.
	const shadowsAppFunction = "pub function save(source: string): i32 {\n  return source.len();\n}\n"
	const appSource = "function source(): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", \"x\");\n  return main();\n}\n"
	// A method call reaches the method of its receiver's type, not each method with that name.
	const callsOwnMethod = "pub struct Counter { n: i32 }\nfunction (c: Counter) size(): i32 {\n  return c.n;\n}\npub function save(s: string): i32 {\n  let c: Counter = Counter { n: s.len() };\n  return c.size();\n}\n"
	const appMethod = "struct Disk { path: string }\nfunction (d: Disk) size(): i32 {\n  write_file(d.path, \"x\");\n  return 0;\n}\n"
	const deniedSave = "error helper fs write_file save → write_file lib.fern:1:5"

	for _, c := range []struct {
		name      string
		depEntry  string
		helper    string
		wantBuild bool
		appExtra  string
		wantE070  string
	}{
		{"granted", "helper = { path = \"../helper\", capabilities = [\"fs\"] }", reachesFS, true, "", ""},
		{"outside-grant", "helper = { path = \"../helper\", capabilities = [\"net\"] }", reachesFS, false, "", deniedSave},
		{"empty-grant", "helper = { path = \"../helper\", capabilities = [] }", reachesFS, false, "", deniedSave},
		{"ungoverned", "helper = { path = \"../helper\" }", reachesFS, true, "", ""},
		{"reaches-nothing", "helper = { path = \"../helper\", capabilities = [] }", reachesNothing, true, "", ""},
		{"parameter-shadows-function", "helper = { path = \"../helper\", capabilities = [] }", shadowsAppFunction, true, appSource, ""},
		{"method-of-another-type", "helper = { path = \"../helper\", capabilities = [] }", callsOwnMethod, true, appMethod, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			proj := t.TempDir()
			entry := writeCapsProject(t, proj, c.depEntry, c.helper, c.appExtra)

			oracleCmd := exec.Command(oracleBin, "-check", entry)
			oracleOut, _ := oracleCmd.CombinedOutput()
			oracleBuilt := oracleCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-o", filepath.Join(proj, "selfhost.bin"), entry, stdlib)
			shOut, _ := shCmd.CombinedOutput()
			shBuilt := shCmd.ProcessState.ExitCode() == 0

			if oracleBuilt != c.wantBuild {
				t.Fatalf("Go built = %v, want %v\n%s", oracleBuilt, c.wantBuild, oracleOut)
			}
			if shBuilt != oracleBuilt {
				t.Errorf("Go built = %v, self-host built = %v\n--- Go ---\n%s\n--- self-host ---\n%s",
					oracleBuilt, shBuilt, oracleOut, shOut)
			}
			want := capsDiagnostics(string(oracleOut))
			got := capsDiagnostics(string(shOut))
			if !c.wantBuild && strings.Join(want, "; ") != c.wantE070 {
				t.Errorf("Go's E070 = %v\n%s", want, oracleOut)
			}
			if strings.Join(want, "; ") != strings.Join(got, "; ") {
				t.Errorf("capability diagnostics differ:\nGo        %v\nself-host %v\n--- Go ---\n%s\n--- self-host ---\n%s",
					want, got, oracleOut, shOut)
			}
		})
	}
}

func TestSelfHostCapsTraitDefaultDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("package capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	proj := t.TempDir()
	for name, src := range map[string]string{
		"app/fern.toml": "[package]\nname = \"app\"\n[dependencies]\nb = { path = \"../b\", capabilities = [] }\n",
		"app/main.fern": "import \"b\";\nstruct R { n: i32 }\nimpl b.Leaky for R {\n  function size(self: Self): i32 { return self.n; }\n}\nfunction main(): i32 {\n  let r: R = R { n: 1 };\n  return r.grab();\n}\n",
		"b/fern.toml":   "[package]\nname = \"b\"\n",
		"b/lib.fern":    "pub trait Leaky {\n  function size(self: Self): i32;\n  function grab(self: Self): i32 {\n    if let Ok(t) = read_file(\"/etc/hostname\") {\n      return t.len();\n    }\n    return self.size();\n  }\n}\n",
	} {
		p := filepath.Join(proj, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(proj, "app", "main.fern")
	oracleOut, _ := exec.Command(buildFernCLIBin(t), "-check", entry).CombinedOutput()
	shOut, _ := exec.Command(driverBin, "-o", filepath.Join(proj, "selfhost.bin"), entry, stdlib).CombinedOutput()
	const want = "error b fs read_file grab → read_file lib.fern:3:3"
	if got := strings.Join(capsDiagnostics(string(oracleOut)), "; "); got != want {
		t.Errorf("Go E070 = %q, want %q\n%s", got, want, oracleOut)
	}
	if got := strings.Join(capsDiagnostics(string(shOut)), "; "); got != want {
		t.Errorf("self-host E070 = %q, want %q\n%s", got, want, shOut)
	}
}

func TestSelfHostCapabilitiesReportAgreesWithNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("capabilities report differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	oracleBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	proj := t.TempDir()
	entry := writeCapsProject(t, proj, "helper = { path = \"../helper\" }",
		"pub function save(s: string): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", s);\n  tcp_close(0);\n  return 0;\n}\n")

	oracleOut, err := exec.Command(oracleBin, "-capabilities", entry).CombinedOutput()
	if err != nil {
		t.Fatalf("Go -capabilities: %v\n%s", err, oracleOut)
	}
	shOut, err := exec.Command(driverBin, "-capabilities", entry, stdlib).CombinedOutput()
	if err != nil {
		t.Fatalf("self-host -capabilities: %v\n%s", err, shOut)
	}
	if want, got := reportRows(string(oracleOut)), reportRows(string(shOut)); want != got {
		t.Errorf("report rows differ:\nGo        %q\nself-host %q\n--- Go ---\n%s\n--- self-host ---\n%s",
			want, got, oracleOut, shOut)
	}
	if !strings.Contains(reportRows(string(oracleOut)), "helper fs,net") {
		t.Errorf("Go report did not attribute fs,net to helper: %q", oracleOut)
	}
}

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

func TestSelfHostCapsModuleBelowPackageRoot(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("package capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	oracleBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	const helper = "pub function save(s: string): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", s);\n  return 0;\n}\n"
	const viaHelper = "import \"helper\";\npub function run(): i32 {\n  return helper.save(\"x\");\n}\n"
	for _, layout := range []struct {
		name  string
		files map[string]string
		entry string
	}{
		{"imported-from-subdir", map[string]string{
			"app/main.fern":    "import \"./lib/mid\";\nfunction main(): i32 {\n  return mid.run();\n}\n",
			"app/lib/mid.fern": viaHelper,
		}, "app/main.fern"},
		{"entry-in-subdir", map[string]string{
			"app/lib/main.fern": "import \"helper\";\nfunction main(): i32 {\n  return helper.save(\"x\");\n}\n",
		}, "app/lib/main.fern"},
		{"dependency-sibling", map[string]string{
			"app/main.fern":     "import \"helper\";\nfunction main(): i32 {\n  return helper.save(\"x\");\n}\n",
			"helper/lib.fern":   "import \"./store\";\npub function save(s: string): i32 {\n  return store.write(s);\n}\n",
			"helper/store.fern": "pub function write(s: string): i32 {\n  write_file(\"/tmp/fern-caps-e2e.txt\", s);\n  return 0;\n}\n",
		}, "app/main.fern"},
	} {
		for _, grant := range []struct {
			caps      string
			wantBuild bool
		}{{`["fs"]`, true}, {`["net"]`, false}} {
			t.Run(layout.name+"/"+grant.caps, func(t *testing.T) {
				proj := t.TempDir()
				files := map[string]string{
					"app/fern.toml":    "[package]\nname = \"app\"\n[dependencies]\nhelper = { path = \"../helper\", capabilities = " + grant.caps + " }\n",
					"helper/fern.toml": "[package]\nname = \"helper\"\n",
					"helper/lib.fern":  helper,
				}
				for rel, src := range layout.files {
					files[rel] = src
				}
				for rel, src := range files {
					p := filepath.Join(proj, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				entry := filepath.Join(proj, filepath.FromSlash(layout.entry))

				oracleOut, err := exec.Command(oracleBin, "-capabilities", entry).CombinedOutput()
				if err != nil {
					t.Fatalf("Go -capabilities: %v\n%s", err, oracleOut)
				}
				shOut, err := exec.Command(driverBin, "-capabilities", entry, stdlib).CombinedOutput()
				if err != nil {
					t.Fatalf("self-host -capabilities: %v\n%s", err, shOut)
				}
				want, got := reportRows(string(oracleOut)), reportRows(string(shOut))
				if want != got {
					t.Errorf("report rows differ:\nGo        %q\nself-host %q", want, got)
				}
				if !strings.Contains(want, "helper fs") {
					t.Errorf("Go report did not attribute fs to helper: %q", oracleOut)
				}

				build := exec.Command(driverBin, "-o", filepath.Join(proj, "app.bin"), entry, stdlib)
				out, _ := build.CombinedOutput()
				if built := build.ProcessState.ExitCode() == 0; built != grant.wantBuild {
					t.Fatalf("self-host built = %v, want %v under grant %s\n%s", built, grant.wantBuild, grant.caps, out)
				}
				if !grant.wantBuild && !strings.Contains(string(out), `error[E070]: package "helper" reaches 'fs'`) {
					t.Errorf("refused without naming helper's fs reach:\n%s", out)
				}
			})
		}
	}
}
