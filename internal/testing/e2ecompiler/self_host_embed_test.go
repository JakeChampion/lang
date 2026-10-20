package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostEmbedBundleAndSubstitution exercises the self-host's `-embed`
// asset bundle (compiler/embed.fern, #6643) — the port of native's
// internal/pkg/embed plus the asset half of internal/check/constfold.
//
// The driver builds a real directory under temp_dir and walks it, because
// sorted order, nested names and the empty-vs-absent bundle are properties of
// the walk rather than of a hand-built value.
//
// Exit 0 means every assertion held. A non-zero code identifies the case, so a
// regression names itself without a stdout diff.
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

// embedBytesBlob is an asset no string could hold: an interior NUL, a valid
// two-byte sequence, a lone 0xff, an overlong encoding, a UTF-16 surrogate and
// a lead byte past U+10FFFF.
var embedBytesBlob = []byte{'A', 0x00, 0xc3, 0xa9, 0xff, 0xc0, 0x80, 0xed, 0xa0, 0x80, 0xf5, 'z'}

// embedBytesSource reads embedBytesBlob back as `u8[]` every way the bytes
// forms allow — a const, a call in a body, the enumeration — and requires it
// byte-exact; it then grows and rewrites its copy and requires the asset
// unchanged on the next evaluation, because an asset is shared static data.
// The text asset beside it comes back through the text form as a string.
// Exit 0 is success; any other code names the failed check.
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

// TestSelfHostEmbedBytesEveryTarget compiles embedBytesSource with `-embed`
// for each target under the strict IR path, the sanitizer and the leak
// census, and requires a clean exit and a balanced census.
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

// TestSelfHostEmbedBytesCompileLinear pins compiling a bytes asset to time
// linear in its size. Each byte is one element of one u8[] literal, so a big
// asset is one block of that many instructions, and a lookup that scanned a
// block's whole step list per instruction made it quadratic: 100 KB took 61 s.
// The gate is a ratio of compile CPU times on the same machine, each the least
// of five compiles. For 8x the bytes the linear compile measures 6.9-7.1x on an
// idle x86-64 runner and up to 9.7x beside the shard's other tests, which
// inflate the larger, memory-bound compile more; the quadratic form cost about
// 25x. The limit sits between the two.
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

// TestSelfHostEmbedMatchesNative is the parity gate: the same source, the same
// embed directory, and the same answer out of both compilers.
//
// Compared on the COMPILED PROGRAM's exit code rather than on the compilers'
// output, because that is what the feature promises — the bytes of a file
// reaching the running program — and because the two word a diagnostic
// differently. What the accept/reject rows assert is only that they agree on
// whether the program is a program at all; matching an exact message across
// two compilers is a diagnostic-format comparison, not an asset one.
//
// The rejections matter most here. A missing asset that compiled into an
// empty string would be a program that builds, runs, and serves nothing —
// which is exactly what an embedded stdlib must never do (#6643).
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

	// The bundle both compilers read. Nested, unsorted on disk, and with a
	// name that is a strict prefix of nothing so `suggest` has a real choice.
	assets := filepath.Join(dir, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	for name, body := range map[string][]byte{
		"z.txt":     []byte("ZZZ"),
		"a.txt":     []byte("AAA"),
		"sub/b.txt": []byte("BB"),
		// The essential asset, and the reason this bundle is written as
		// bytes: an interior NUL and a byte past 0x7f, which is what an image,
		// a font or a wasm module looks like. Fern's `read_file` validates
		// UTF-8 and refuses this file outright, so a walk built on it embeds
		// text and rejects every directory holding anything else — a
		// divergence a text-only fixture cannot see.
		"blob.bin": {'A', 0x00, 'B', 0xff, 'C'},
	} {
		if err := os.WriteFile(filepath.Join(assets, filepath.FromSlash(name)), body, 0o644); err != nil {
			t.Fatalf("write asset %s: %v", name, err)
		}
	}
	// An empty directory is a legitimate bundle, and not the same thing as
	// having passed no -embed at all.
	empty := filepath.Join(dir, "empty-assets")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	// A name reaches the program as a string, so a file whose name is not
	// UTF-8 makes the whole directory unembeddable.
	badName := filepath.Join(dir, "bad-name-assets")
	if err := os.MkdirAll(badName, 0o755); err != nil {
		t.Fatalf("mkdir bad-name: %v", err)
	}
	haveBadName := os.WriteFile(filepath.Join(badName, "bad\xff.txt"), []byte("x"), 0o644) == nil

	// Symlinks. Entries are SKIPPED — that is what keeps an asset tree from
	// reaching outside its root or wedging the walk on a cycle — while the
	// ROOT is FOLLOWED, because it is the path the user typed and
	// `-embed ./link-to-assets` plainly means the directory behind it. Native
	// draws the line in the same place, with os.Stat on the root and WalkDir's
	// lstat below it; the self-host needed `lstat` (#7982) to draw it at all.
	//
	// The link that escapes the root is the essential one: a walk that
	// followed it would embed a file the user never put in the bundle.
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
		// A link to the root's own subdirectory: following it would both
		// duplicate sub/b.txt under a second name and, on a self-referential
		// tree, never terminate.
		if err := os.Symlink(filepath.Join(assets, "sub"), filepath.Join(assets, "loop")); err != nil {
			t.Fatalf("symlink loop: %v", err)
		}
		if err := os.Symlink(assets, linkedRoot); err != nil {
			t.Fatalf("symlink root: %v", err)
		}
	}

	for _, c := range []struct {
		name string
		// embedDir is passed to -embed; empty means the flag is omitted.
		embedDir string
		src      string
		// wantExit is the compiled program's exit code, and -1 means the
		// program must not compile at all.
		wantExit int
	}{
		// 3 + 2 = 5: the bytes of two files, one of them nested.
		{"asset-one", assets, "function main(): i32 {\n    return __fern_asset(\"a.txt\").len() + __fern_asset(\"sub/b.txt\").len();\n}\n", 5},
		// Names sorted, contents alongside: 5+8+9+5 name bytes, 3+5+2+3
		// contents, the 5 being the binary blob as u8[].
		{"assets-all", assets, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets_bytes() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 40},
		// Sorted order is observable, and it is what keeps the emitted program
		// identical across hosts — a filesystem-order walk would not be.
		{"assets-sorted", assets, "function main(): i32 {\n    let first: string = \"\";\n    for a in __fern_assets_bytes() { if (first.len() == 0) { first = a.0; } }\n    if (first == \"a.txt\") { return 7; }\n    return 9;\n}\n", 7},
		// A bundle of text enumerates as strings: 5 name bytes + 2 contents.
		{"assets-text", filepath.Join(assets, "sub"), "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 7},
		// An empty bundle yields an empty typed array, so the loop body simply
		// never runs. The element type has to be stamped for that to compile.
		{"assets-empty-bundle", empty, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", 0},
		// A symlinked entry is not in the bundle at all, so naming it is the
		// ordinary unknown-asset refusal. This is the sharp form of the skip:
		// `escape.txt` points OUTSIDE the embed root, so a walk that followed
		// it would compile a file the user never put in the bundle — and the
		// row above proves the same skip leaves the count unchanged, since
		// `assets-all` still sums to 40 with two symlinks in the directory.
		{"symlinked-entry-is-not-an-asset", assets, "function main(): i32 {\n    return __fern_asset(\"escape.txt\").len();\n}\n", -1},
		// Naming a symlinked directory on the command line embeds what it
		// points at, and gives the same bundle as naming the directory itself.
		{"assets-follow-symlinked-root", "SYMLINKED_ROOT", "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets_bytes() { n = n + a.0.len() + a.1.len(); }\n    return n;\n}\n", 40},
		// Binary bytes, asserted per byte: nothing truncated at the NUL and
		// nothing rewrote the 0xff. The length alone would not catch either,
		// since a walk that mangled the high byte in place keeps the count.
		{"asset-binary", assets, "function main(): i32 {\n    let b: u8[] = __fern_asset_bytes(\"blob.bin\");\n    if (b.len() != 5) { return 1; }\n    if (b[0] != 65u8 || b[1] != 0u8 || b[2] != 66u8) { return 2; }\n    if (b[3] != 255u8 || b[4] != 67u8) { return 3; }\n    return 0;\n}\n", 0},
		// A string is always valid UTF-8, so neither text form takes the blob.
		{"asset-text-refuses-binary", assets, "function main(): i32 {\n    return __fern_asset(\"blob.bin\").len();\n}\n", -1},
		{"assets-text-refuses-binary", assets, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", -1},
		{"non-utf8-name", "BAD_NAME", "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets_bytes() { n = n + 1; }\n    return n;\n}\n", -1},

		// Every refusal. Each of these is a program that would otherwise build
		// with a silently empty asset in it.
		{"no-embed-flag", "", "function main(): i32 {\n    return __fern_asset(\"a.txt\").len();\n}\n", -1},
		{"no-embed-flag-enumerate", "", "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets() { n = n + 1; }\n    return n;\n}\n", -1},
		{"unknown-name", assets, "function main(): i32 {\n    return __fern_asset(\"a.tx\").len();\n}\n", -1},
		{"computed-name", assets, "function main(): i32 {\n    let n: string = \"a.txt\";\n    return __fern_asset(n).len();\n}\n", -1},
		{"wrong-arity", assets, "function main(): i32 {\n    return __fern_asset(\"a.txt\", \"z.txt\").len();\n}\n", -1},
		{"enumerate-with-argument", assets, "function main(): i32 {\n    let n: i32 = 0;\n    for a in __fern_assets(\"a.txt\") { n = n + 1; }\n    return n;\n}\n", -1},
		// A nested call is refused on the OUTER argument not being a literal.
		// Both passes claim an asset call before descending into it, so the
		// inner one never resolves into a name the outer could then accept —
		// which a post-order walk would have compiled into a real lookup.
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
			// Every bundle shape takes the typed path, the empty one included.
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
			// The oracle's interpreter reads the same bundle the same way.
			interp := exec.Command(nativeBin, "-embed", c.embedDir, "-interp", src)
			interpOut, _ := interp.CombinedOutput()
			if code := interp.ProcessState.ExitCode(); code != c.wantExit {
				t.Errorf("oracle interpreter exited %d, want %d\n%s", code, c.wantExit, interpOut)
			}
		})
	}
}

// TestSelfHostEmbedCarriesTheStdlib is what #6643 actually needs: the whole
// internal/stdlib tree inside one module, reachable by name at runtime.
//
// A wasm-hosted compiler has no host filesystem to read the stdlib from, and
// the self-host CLI's stdlib root is a path on the command line — so until
// this works there is no way for the self-host compiler to ship the stdlib the
// way native's go:embed does. The size is asserted loosely, only that the
// bundle is actually IN the artifact: an embed that silently dropped the files
// would still enumerate zero and still exit 0 without the count check.
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

	// Counts the `.fern` modules and asserts one of them arrived intact, so a
	// walk that returned names without contents cannot pass.
	src := filepath.Join(dir, "embed_stdlib.fern")
	prog := "function main(): i32 {\n" +
		"    let n: i32 = 0;\n" +
		"    let io_len: i32 = 0;\n" +
		"    for a in __fern_assets() {\n" +
		"        if (a.0.len() > 5 && a.0[a.0.len() - 5] == b'.') { n = n + 1; }\n" +
		"        if (a.0 == \"std/io.fern\") { io_len = a.1.len(); }\n" +
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
		t.Fatalf("fewer than 60 .fern modules reached the binary — the walk lost files\n%s", out)
	case 2:
		t.Fatalf("std/io.fern arrived with no contents — the walk kept names but not bytes\n%s", out)
	default:
		t.Fatalf("stdlib-embedding program exited %d\n%s", code, out)
	}
}
