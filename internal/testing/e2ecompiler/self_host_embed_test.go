package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostEmbedBundleAndSubstitution(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("embed_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/embed_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/embed_run.fern", "embed_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("embed_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("embed_run exit code = %d, want 0 — that code is the failing assertion's id in embed_run.fern\n%s", code, out)
	}
	if want := "embed: bundle and substitution agree"; !strings.Contains(string(out), want) {
		t.Errorf("embed_run stdout = %q, want it to contain %q", out, want)
	}
}

var embedBytesBlob = []byte{'A', 0x00, 0xc3, 0xa9, 0xff, 0xc0, 0x80, 0xed, 0xa0, 0x80, 0xf5, 'z'}

const embedBytesSource = `const WANT: u8[] = [65u8, 0u8, 195u8, 169u8, 255u8, 192u8, 128u8, 237u8, 160u8, 128u8, 245u8, 122u8];
const BLOB: u8[] = __fern_asset_bytes("bin/blob.bin");

function same(b: u8[]): boolean {
  if (b.len() != WANT.len()) {
    return false;
  }
  let i: i32 = 0;
  while (i < b.len()) {
    if (b[i] != WANT[i]) {
      return false;
    }
    i = i + 1;
  }
  return true;
}

function main(): i32 {
  if (!same(BLOB)) {
    return 1;
  }
  let b: u8[] = __fern_asset_bytes("bin/blob.bin");
  if (!same(b)) {
    return 2;
  }
  let grown: u8[] = b.append(7u8).with(0, 66u8);
  if (grown.len() != 13 || grown[0] != 66u8 || !same(b)) {
    return 3;
  }
  if (!same(__fern_asset_bytes("bin/blob.bin"))) {
    return 4;
  }
  let seen: i32 = 0;
  for a in __fern_assets_bytes() {
    if (a.0 == "bin/blob.bin" && same(a.1)) {
      seen = seen + 1;
    }
    if (a.0 == "note.txt" && a.1.len() == 6) {
      seen = seen + 10;
    }
  }
  if (seen != 11) {
    return 5;
  }
  if (__fern_asset("note.txt") != "h\u{e9}llo") {
    return 6;
  }
  return 0;
}
`

func TestSelfHostEmbedBytesEveryTarget(t *testing.T) {
	assets := t.TempDir()
	if err := os.MkdirAll(filepath.Join(assets, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "bin", "blob.bin"), embedBytesBlob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "note.txt"), []byte("héllo"), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	cli.flags = []string{"-embed", assets}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, embedBytesSource, target, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit = %d, want 0 — that code is the failing check in embedBytesSource\n%s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostEmbedBytesCompileLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles two large assets; skipped under -short")
	}
	assets := t.TempDir()
	const small, large = 20_000, 160_000
	for name, n := range map[string]int{"small.bin": small, "large.bin": large} {
		body := make([]byte, n)
		for i := range body {
			body[i] = byte(i*131 + i/7)
		}
		if err := os.WriteFile(filepath.Join(assets, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cli := buildSelfHostCLI(t)
	cli.flags = []string{"-embed", assets}
	cpu := func(name string) float64 {
		src := filepath.Join(t.TempDir(), "main.fern")
		prog := "function main(): i32 {\n  return __fern_asset_bytes(\"" + name + "\").len() % 7;\n}\n"
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		least := 0.0
		for i := 0; i < 5; i++ {
			cmd := cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "out.s"), src, "-emit", "asm")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile %s: %v\n%s", name, err, out)
			}
			sec := (cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()
			if i == 0 || sec < least {
				least = sec
			}
		}
		return least
	}
	s, l := cpu("small.bin"), cpu("large.bin")
	t.Logf("compile CPU: %d bytes %.2fs, %d bytes %.2fs", small, s, large, l)
	if l > 12*s {
		t.Errorf("8x the asset took %.1fx the compile time (%.2fs vs %.2fs) — compiling one long block is superlinear again", l/s, l, s)
	}
}

func TestSelfHostEmbedMatchesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("embed differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	for name, body := range map[string][]byte{
		"z.txt":     []byte("ZZZ"),
		"a.txt":     []byte("AAA"),
		"sub/b.txt": []byte("BB"),
		"blob.bin":  {'A', 0x00, 'B', 0xff, 'C'},
	} {
		if err := os.WriteFile(filepath.Join(assets, filepath.FromSlash(name)), body, 0o644); err != nil {
			t.Fatalf("write asset %s: %v", name, err)
		}
	}
	empty := filepath.Join(dir, "empty-assets")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	badName := filepath.Join(dir, "bad-name-assets")
	if err := os.MkdirAll(badName, 0o755); err != nil {
		t.Fatalf("mkdir bad-name: %v", err)
	}
	haveBadName := os.WriteFile(filepath.Join(badName, "bad\xff.txt"), []byte("x"), 0o644) == nil

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	haveSymlinks := true
	if err := os.Symlink(outside, filepath.Join(assets, "escape.txt")); err != nil {
		haveSymlinks = false
		t.Logf("symlinks unavailable, skipping those rows: %v", err)
	}
	linkedRoot := filepath.Join(dir, "linked-assets")
	if haveSymlinks {
		if err := os.Symlink(filepath.Join(assets, "sub"), filepath.Join(assets, "loop")); err != nil {
			t.Fatalf("symlink loop: %v", err)
		}
		if err := os.Symlink(assets, linkedRoot); err != nil {
			t.Fatalf("symlink root: %v", err)
		}
	}

	for _, c := range []struct {
		name     string
		embedDir string
		src      string
		wantExit int
	}{
		{"asset-one", assets, "function main(): i32 {\n    return __fern_asset(\"a.txt\").len() + __fern_asset(\"sub/b.txt\").len();\n}\n", 5},
		{"assets-all", assets, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets_bytes() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 40},
		{"assets-sorted", assets, "function main(): i32 {\n    let first: string = \"\";\n    for a in __fern_assets_bytes() { if (first.len() == 0) { first = a.0; } }\n    if (first == \"a.txt\") { return 7; }\n    return 9;\n}\n", 7},
		{"assets-text", filepath.Join(assets, "sub"), "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 7},
		{"assets-empty-bundle", empty, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", 0},
		{"symlinked-entry-is-not-an-asset", assets, "function main(): i32 {\n    return __fern_asset(\"escape.txt\").len();\n}\n", -1},
		{"assets-follow-symlinked-root", "SYMLINKED_ROOT", "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets_bytes() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 40},
		{"asset-binary", assets, "function main(): i32 {\n    let b: u8[] = __fern_asset_bytes(\"blob.bin\");\n    if (b.len() != 5) { return 1; }\n    if (b[0] != 65u8 || b[1] != 0u8 || b[2] != 66u8) { return 2; }\n    if (b[3] != 255u8 || b[4] != 67u8) { return 3; }\n    return 0;\n}\n", 0},
		{"asset-text-refuses-binary", assets, "function main(): i32 {\n    return __fern_asset(\"blob.bin\").len();\n}\n", -1},
		{"assets-text-refuses-binary", assets, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", -1},
		{"non-utf8-name", "BAD_NAME", "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets_bytes() { n = n + 1; }\n    return n;\n}\n", -1},

		{"no-embed-flag", "", "function main(): i32 {\n    return __fern_asset(\"a.txt\").len();\n}\n", -1},
		{"no-embed-flag-enumerate", "", "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", -1},
		{"unknown-name", assets, "function main(): i32 {\n    return __fern_asset(\"a.tx\").len();\n}\n", -1},
		{"computed-name", assets, "function main(): i32 {\n    let n: string = \"a.txt\";\n    return __fern_asset(n).len();\n}\n", -1},
		{"wrong-arity", assets, "function main(): i32 {\n    return __fern_asset(\"a.txt\", \"z.txt\").len();\n}\n", -1},
		{"enumerate-with-argument", assets, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets(\"a.txt\") { n = n + 1; }\n    return n;\n}\n", -1},
		{"nested-asset-name", assets, "function main(): i32 {\n    return __fern_asset(__fern_asset(\"a.txt\")).len();\n}\n", -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.embedDir == "BAD_NAME" {
				if !haveBadName {
					t.Skip("this filesystem refuses a non-UTF-8 name")
				}
				c.embedDir = badName
			} else if c.embedDir == "SYMLINKED_ROOT" {
				if !haveSymlinks {
					t.Skip("symlinks unavailable")
				}
				c.embedDir = linkedRoot
			} else if !haveSymlinks && c.name == "symlinked-entry-is-not-an-asset" {
				t.Skip("symlinks unavailable")
			}
			src := filepath.Join(dir, "embed_"+c.name+".fern")
			if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			outDir := t.TempDir()

			nativeArgs := []string{"-target", "x86-64-linux", "-o", filepath.Join(outDir, "n")}
			shArgs := []string{"-target", "x86-64-linux", "-o", filepath.Join(outDir, "s")}
			if c.embedDir != "" {
				nativeArgs = append(nativeArgs, "-embed", c.embedDir)
				shArgs = append(shArgs, "-embed", c.embedDir)
			}
			nativeCmd := exec.Command(nativeBin, append(nativeArgs, src)...)
			nativeBuild, _ := nativeCmd.CombinedOutput()
			shCmd := exec.Command(driverBin, append(shArgs, src, stdlib)...)
			shBuild, _ := shCmd.CombinedOutput()

			nativeOK := nativeCmd.ProcessState.ExitCode() == 0
			shOK := shCmd.ProcessState.ExitCode() == 0
			if nativeOK != shOK {
				t.Fatalf("compilers disagree on whether this builds: native ok=%v, self-host ok=%v\n--- native ---\n%s\n--- self-host ---\n%s",
					nativeOK, shOK, nativeBuild, shBuild)
			}
			if c.wantExit < 0 {
				if nativeOK {
					t.Fatalf("both compilers accepted a program that must be refused\n--- native ---\n%s\n--- self-host ---\n%s", nativeBuild, shBuild)
				}
				return
			}
			if !nativeOK {
				t.Fatalf("both compilers refused a program that must build\n--- native ---\n%s\n--- self-host ---\n%s", nativeBuild, shBuild)
			}

			nativeRun := exec.Command(filepath.Join(outDir, "n"))
			nativeRunOut, _ := nativeRun.CombinedOutput()
			shRun := exec.Command(filepath.Join(outDir, "s"))
			shRunOut, _ := shRun.CombinedOutput()
			nativeCode := nativeRun.ProcessState.ExitCode()
			shCode := shRun.ProcessState.ExitCode()
			if nativeCode != c.wantExit {
				t.Errorf("native-compiled program exited %d, want %d\n%s", nativeCode, c.wantExit, nativeRunOut)
			}
			if shCode != c.wantExit {
				t.Errorf("self-host-compiled program exited %d, want %d\n%s", shCode, c.wantExit, shRunOut)
			}
			interp := exec.Command(nativeBin, "-embed", c.embedDir, "-interp", src)
			interpOut, _ := interp.CombinedOutput()
			if code := interp.ProcessState.ExitCode(); code != c.wantExit {
				t.Errorf("oracle interpreter exited %d, want %d\n%s", code, c.wantExit, interpOut)
			}
		})
	}
}

func TestSelfHostEmbedCarriesTheStdlib(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the self-host CLI; skipped under -short")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stdlib embed check runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}

	src := filepath.Join(dir, "embed_stdlib.fern")
	prog := "import \"std/string\";\nfunction main(): i32 {\n" +
		"    let n: i32 = 0;\n" +
		"    let io_len: i32 = 0;\n" +
		"    for a in __fern_assets() {\n" +
		"        if (a.0.ends_with(\".fern\") || a.0.ends_with(\".fern.md\")) { n = n + 1; }\n" +
		"        if (a.0 == \"std/io.fern.md\") { io_len = a.1.len(); }\n" +
		"    }\n" +
		"    if (n < 60) { return 1; }\n" +
		"    if (io_len < 100) { return 2; }\n" +
		"    return 0;\n" +
		"}\n"
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "stdemb")
	build := exec.Command(driverBin, "-target", "x86-64-linux", "-embed", stdlib, "-o", bin, src, stdlib)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("self-host could not embed the stdlib: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	out, _ := run.CombinedOutput()
	switch code := run.ProcessState.ExitCode(); code {
	case 0:
	case 1:
		t.Fatalf("fewer than 60 Fern modules reached the binary — the walk lost files\n%s", out)
	case 2:
		t.Fatalf("std/io.fern.md arrived with no contents — the walk kept names but not bytes\n%s", out)
	default:
		t.Fatalf("stdlib-embedding program exited %d\n%s", code, out)
	}
}
