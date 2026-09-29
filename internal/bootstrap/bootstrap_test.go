package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fixpointCompiler is a stand-in compiler whose output IS itself, so every
// generation it builds is byte-identical to the one before: the shape a real
// stage chain must have. It emits a tr stand-in for the multi-module smoke
// source; other output is itself, exiting 42 when run without -o.
const fixpointCompiler = `#!/bin/sh
out=""
tr_source=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    */coreutils/tr.fern) tr_source="$1"; shift ;;
    *) shift ;;
  esac
done
[ -n "$out" ] || exit 42
if [ -n "$tr_source" ]; then
  [ -f "$tr_source" ] || exit 1
  cat > "$out" <<'PROGRAM'
#!/bin/sh
exec tr "$@"
PROGRAM
  exit 0
fi
cp "$0" "$out"
`

// brokenOutputCompiler compiles, but everything it produces exits 1 — a
// compiler whose output links and does not work.
const brokenOutputCompiler = `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
done
[ -n "$out" ] || exit 1
cp "$0" "$out"
`

// oneLinerCompiler's output runs the one-line smoke program and nothing else:
// run as coreutils/tr it exits 42 too, like a stage1 that aborts on any
// real program.
const oneLinerCompiler = `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in -o) out="$2"; shift 2 ;; *) shift ;; esac
done
[ -n "$out" ] || exit 42
cp "$0" "$out"
`

// driftingCompiler appends a line to itself on every generation, so each
// stage differs from the one before by exactly one line.
const driftingCompiler = fixpointCompiler + `echo "# one more generation" >> "$out"
`

// sourceCompiler compiles the entry source literally: handed a fern.fern, its
// output is that file, so what a generation does is decided by the SOURCE it
// was built from, as with the real compiler, and not inherited from the
// compiler that built it. Any other program compiles to a copy of itself.
const sourceCompiler = `#!/bin/sh
out=""
src=""
tr_source=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    */coreutils/tr.fern) tr_source="$1"; shift ;;
    */fern.fern) src="$1"; shift ;;
    *) shift ;;
  esac
done
[ -n "$out" ] || exit 42
if [ -n "$tr_source" ]; then
  [ -f "$tr_source" ] || exit 1
  cat > "$out" <<'PROGRAM'
#!/bin/sh
exec tr "$@"
PROGRAM
  exit 0
fi
if [ -n "$src" ]; then cp "$src" "$out"; else cp "$0" "$out"; fi
`

// olderPinCompiler is sourceCompiler with an older code generator: it emits
// the same program with one extra trailing line, as a pin built before a
// codegen change does.
const olderPinCompiler = sourceCompiler + `echo "# an older code generator emitted this" >> "$out"
`

// failingCompiler stands in for an old stage0 meeting a construct it does not
// know: it refuses to compile at all.
const failingCompiler = `#!/bin/sh
echo "fake compiler: parse error" >&2
exit 1
`

// hostName mirrors the script's own host detection, so the tests can pin the
// lock line the script will read on whichever supported host runs them.
func hostName(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "x86-64-linux"
	case "linux/arm64":
		return "arm64-linux"
	case "darwin/arm64":
		return "arm64-darwin"
	}
	t.Skipf("no bootstrap host for %s/%s", runtime.GOOS, runtime.GOARCH)
	return ""
}

// checkout lays out the slice of a repository the script touches — its own
// copy under bootstrap/, the entry file and the stdlib root — in a temp dir,
// so a run cannot write into the real build/ or bin/.
func checkout(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	_, here, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "..", "bootstrap", "bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, d := range []string{"bootstrap", "examples/self_host", "internal/stdlib", "coreutils"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "bootstrap", "bootstrap.sh"), script, 0o755)
	write(t, filepath.Join(root, "examples", "self_host", "fern.fern"), []byte("function main(): i32 { return 0; }\n"), 0o644)
	write(t, filepath.Join(root, "coreutils", "tr.fern"), []byte("// Multi-module smoke source for the stand-in compiler.\n"), 0o644)
	return root
}

func write(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// run executes the script's mode in root with extra environment and PATH
// prefix, returning combined output and the exit error (nil on success).
func run(t *testing.T, root, mode string, env []string, pathPrefix string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root, "bootstrap", "bootstrap.sh"), mode)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	if pathPrefix != "" {
		cmd.Env = append(cmd.Env, "PATH="+pathPrefix+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	out, err := cmd.CombinedOutput()
	t.Logf("bootstrap.sh output:\n%s", out)
	return string(out), err
}

func TestBuildInstallsSmokeTestedStage1(t *testing.T) {
	root := checkout(t)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(fixpointCompiler), 0o755)

	out, err := run(t, root, "build", []string{"STAGE0=" + stage0}, "")
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if !strings.Contains(out, "smoke: ") || !strings.Contains(out, "installed bin/fern-selfhost") {
		t.Errorf("output does not report the smoke test and the install")
	}
	installed, err := os.ReadFile(filepath.Join(root, "bin", "fern-selfhost"))
	if err != nil {
		t.Fatalf("bin/fern-selfhost not installed: %v", err)
	}
	if !bytes.Equal(installed, []byte(fixpointCompiler)) {
		t.Errorf("installed binary is not stage1's bytes")
	}
	if _, err := os.Stat(filepath.Join(root, "build", "bootstrap", "stage2")); err == nil {
		t.Errorf("build ran stage2; that is distcheck's job")
	}
}

func TestBuildRejectsACompilerWhoseOutputDoesNotRun(t *testing.T) {
	root := checkout(t)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(brokenOutputCompiler), 0o755)

	out, err := run(t, root, "build", []string{"STAGE0=" + stage0}, "")
	if err == nil {
		t.Fatal("a compiler whose programs exit 1 must fail the smoke test")
	}
	if !strings.Contains(out, "smoke:") || !strings.Contains(out, "exited 1, want 42") {
		t.Errorf("failure does not name the smoke test's exit code")
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "fern-selfhost")); err == nil {
		t.Errorf("bin/fern-selfhost installed despite a failed smoke test")
	}
}

func TestBuildRejectsBrokenMultiModuleSmoke(t *testing.T) {
	for _, tc := range []struct {
		name, old, replacement, diagnostic string
	}{
		{"compile", `[ -f "$tr_source" ] || exit 1`, `exit 1`, "could not compile coreutils/tr.fern"},
		{"run", `exec tr "$@"`, `exit 1`, "tr compiled by"},
		// The wrong tr drains its input first: the smoke pipes into it under
		// pipefail, so one that exits unread can fail the pipeline with SIGPIPE
		// before the output is ever compared.
		{"output", `exec tr "$@"`, `cat >/dev/null; printf abc`, "printed 'abc', want 'xyz'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := checkout(t)
			stage0 := filepath.Join(root, "candidate")
			compiler := strings.Replace(fixpointCompiler, tc.old, tc.replacement, 1)
			write(t, stage0, []byte(compiler), 0o755)
			out, err := run(t, root, "build", []string{"STAGE0=" + stage0}, "")
			if err == nil || !strings.Contains(out, tc.diagnostic) {
				t.Fatalf("expected multi-module smoke failure %q, got %v", tc.diagnostic, err)
			}
			if _, err := os.Stat(filepath.Join(root, "bin", "fern-selfhost")); !os.IsNotExist(err) {
				t.Fatalf("compiler must not be installed after failed smoke: %v", err)
			}
		})
	}
}

func TestBuildRejectsACompilerThatOnlyHandlesAOneLiner(t *testing.T) {
	root := checkout(t)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(oneLinerCompiler), 0o755)

	out, err := run(t, root, "build", []string{"STAGE0=" + stage0}, "")
	if err == nil {
		t.Fatal("a compiler whose coreutils/tr does not translate must fail the smoke test")
	}
	if !strings.Contains(out, "smoke: tr compiled by") {
		t.Errorf("failure does not name the tr smoke: %s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "fern-selfhost")); err == nil {
		t.Errorf("bin/fern-selfhost installed despite a failed smoke test")
	}
}

func TestDistcheckReachesFixedPoint(t *testing.T) {
	root := checkout(t)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(fixpointCompiler), 0o755)

	// No prior build: distcheck runs it first.
	out, err := run(t, root, "distcheck", []string{"STAGE0=" + stage0}, "")
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if !strings.Contains(out, "stage2 == stage3") {
		t.Errorf("output does not report the fixed point")
	}
	for _, s := range []string{"stage1", "stage2", "stage3"} {
		if _, err := os.Stat(filepath.Join(root, "build", "bootstrap", s)); err != nil {
			t.Errorf("%s not left in build/bootstrap: %v", s, err)
		}
	}
}

// A pin built before a code generator change emits a stage1 that differs from
// stage2, and that is not a failure: stage1 carries the pin's code generation,
// stage2 and stage3 the current source's. The fixed point is stage2 == stage3.
func TestDistcheckPassesWhenThePinPredatesACodegenChange(t *testing.T) {
	root := checkout(t)
	write(t, filepath.Join(root, "examples", "self_host", "fern.fern"), []byte(sourceCompiler), 0o644)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(olderPinCompiler), 0o755)

	out, err := run(t, root, "distcheck", []string{"STAGE0=" + stage0}, "")
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if !strings.Contains(out, "stage1 != stage2") || !strings.Contains(out, "stage2 == stage3") {
		t.Errorf("output does not report the pin's divergence and the fixed point")
	}
	s1, err1 := os.ReadFile(filepath.Join(root, "build", "bootstrap", "stage1"))
	s2, err2 := os.ReadFile(filepath.Join(root, "build", "bootstrap", "stage2"))
	if err1 != nil || err2 != nil {
		t.Fatalf("stage1/stage2 not kept: %v / %v", err1, err2)
	}
	if bytes.Equal(s1, s2) {
		t.Errorf("stage1 equals stage2; the fake pin did not emit differently")
	}
	if !bytes.Equal(s2, []byte(sourceCompiler)) {
		t.Errorf("stage2 is not the current source's compiler")
	}
}

func TestDistcheckDivergentStagesFail(t *testing.T) {
	root := checkout(t)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(driftingCompiler), 0o755)

	out, err := run(t, root, "distcheck", []string{"STAGE0=" + stage0}, "")
	if err == nil {
		t.Fatal("a drifting compiler must not reach a fixed point")
	}
	if !strings.Contains(out, "stage2 != stage3") {
		t.Errorf("failure does not name the divergence")
	}
	// Both generations stay on disk for the bisection recipe.
	s2, err2 := os.ReadFile(filepath.Join(root, "build", "bootstrap", "stage2"))
	s3, err3 := os.ReadFile(filepath.Join(root, "build", "bootstrap", "stage3"))
	if err2 != nil || err3 != nil {
		t.Fatalf("stage2/stage3 not kept: %v / %v", err2, err3)
	}
	if bytes.Equal(s2, s3) {
		t.Errorf("kept stages are identical; the fake did not drift")
	}
}

func TestStage0RefusingTheSourceFails(t *testing.T) {
	root := checkout(t)
	stage0 := filepath.Join(root, "candidate")
	write(t, stage0, []byte(failingCompiler), 0o755)

	out, err := run(t, root, "build", []string{"STAGE0=" + stage0}, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(out, "stage1 failed") || !strings.Contains(out, "refreshing the pin") {
		t.Errorf("failure does not point at the stage0 refresh")
	}
}

func TestUnknownModeFails(t *testing.T) {
	root := checkout(t)
	out, err := run(t, root, "deploy", nil, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(out, "usage:") {
		t.Errorf("failure does not print the usage")
	}
}

// fakeCurl writes a PATH directory whose `curl` serves one gzip-compressed
// payload for any URL ending in the asset name, recording the URL it was
// asked for. With payload empty every call fails, which is how a test asserts
// that no download happened.
func fakeCurl(t *testing.T, payload []byte) (dir, urlLog string) {
	t.Helper()
	return curlServing(t, payload, "gzip -c", "")
}

// gatedCurl is fakeCurl whose every call waits until n calls have arrived
// before any of them serves, so n concurrent callers are all inside the
// download branch at once. A call that waits ten seconds serves anyway, so a
// caller that never arrives fails the test instead of hanging it.
func gatedCurl(t *testing.T, payload []byte, n int) (dir, urlLog string) {
	t.Helper()
	gate := filepath.Join(t.TempDir(), "arrived")
	if err := os.Mkdir(gate, 0o755); err != nil {
		t.Fatal(err)
	}
	wait := fmt.Sprintf("touch %s/$$\ni=0\nwhile [ \"$(ls %s | wc -l)\" -lt %d ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done\n", gate, gate, n)
	return curlServing(t, payload, "gzip -c", wait)
}

// rawCurl is fakeCurl serving the payload as it is, not gzip-compressed: a
// proxy's error page or a truncated transfer where the release asset should be.
func rawCurl(t *testing.T, payload []byte) (dir, urlLog string) {
	t.Helper()
	return curlServing(t, payload, "cat", "")
}

// curlServing writes the fake curl: it logs the URL, runs prelude, then
// serves the payload through filter into the -o file.
func curlServing(t *testing.T, payload []byte, filter, prelude string) (dir, urlLog string) {
	t.Helper()
	dir = t.TempDir()
	urlLog = filepath.Join(dir, "urls")
	src := filepath.Join(dir, "payload")
	body := "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2 ;; *) url=\"$1\"; shift ;; esac; done\n" +
		"echo \"$url\" >> " + urlLog + "\n" + prelude
	if payload == nil {
		body += "echo 'fake curl: no download expected' >&2; exit 22\n"
	} else {
		write(t, src, payload, 0o644)
		body += filter + " " + src + " > \"$out\"\n"
	}
	write(t, filepath.Join(dir, "curl"), []byte(body), 0o755)
	return dir, urlLog
}

func lock(host, pin string) []byte {
	return []byte("source 0123456789abcdef0123456789abcdef01234567\n" +
		"url https://example.invalid/releases/download/stage0-20260901-0123456\n" +
		host + " " + pin + "\n")
}

func TestPinnedStage0IsDownloadedVerifiedAndCached(t *testing.T) {
	root := checkout(t)
	host := hostName(t)
	write(t, filepath.Join(root, "bootstrap", "stage0.lock"), lock(host, sum([]byte(fixpointCompiler))), 0o644)

	bin, urls := fakeCurl(t, []byte(fixpointCompiler))
	out, err := run(t, root, "build", nil, bin)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	asked, _ := os.ReadFile(urls)
	want := "https://example.invalid/releases/download/stage0-20260901-0123456/fern-selfhost-" + host + ".gz\n"
	if string(asked) != want {
		t.Errorf("downloaded %q, want %q", asked, want)
	}
	if !strings.Contains(out, "stage0-20260901-0123456 for "+host) {
		t.Errorf("output does not name the pinned release")
	}
	cached := filepath.Join(root, "build", "bootstrap", "stage0", "stage0-20260901-0123456", "fern-selfhost-"+host)
	if got, err := os.ReadFile(cached); err != nil || !bytes.Equal(got, []byte(fixpointCompiler)) {
		t.Fatalf("stage0 not cached decompressed at %s: %v", cached, err)
	}

	// Second run: the cache is verified and reused, curl is never called.
	bin, urls = fakeCurl(t, nil)
	if _, err := run(t, root, "build", nil, bin); err != nil {
		t.Fatalf("second run must reuse the cache: %v", err)
	}
	if _, err := os.Stat(urls); err == nil {
		t.Errorf("second run downloaded again")
	}
}

func TestPinnedStage0WithWrongHashIsRejected(t *testing.T) {
	root := checkout(t)
	host := hostName(t)
	write(t, filepath.Join(root, "bootstrap", "stage0.lock"), lock(host, sum([]byte("something else"))), 0o644)

	bin, _ := fakeCurl(t, []byte(fixpointCompiler))
	out, err := run(t, root, "build", nil, bin)
	if err == nil {
		t.Fatal("a hash mismatch must fail")
	}
	if !strings.Contains(out, "sha256 mismatch") {
		t.Errorf("failure does not name the hash mismatch")
	}
	cached := filepath.Join(root, "build", "bootstrap", "stage0", "stage0-20260901-0123456")
	entries, _ := os.ReadDir(cached)
	if len(entries) != 0 {
		t.Errorf("rejected download left behind: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "fern-selfhost")); err == nil {
		t.Errorf("bin/fern-selfhost installed despite a rejected stage0")
	}
}

func TestPinnedStage0ThatIsNotGzipIsRejectedAndCleanedUp(t *testing.T) {
	root := checkout(t)
	host := hostName(t)
	write(t, filepath.Join(root, "bootstrap", "stage0.lock"), lock(host, sum([]byte(fixpointCompiler))), 0o644)

	bin, _ := rawCurl(t, []byte("<html>proxy error</html>"))
	out, err := run(t, root, "build", nil, bin)
	if err == nil {
		t.Fatal("a payload that is not gzip data must fail")
	}
	if !strings.Contains(out, "is not gzip data") {
		t.Errorf("failure does not say the asset is not gzip data")
	}
	cached := filepath.Join(root, "build", "bootstrap", "stage0", "stage0-20260901-0123456")
	entries, _ := os.ReadDir(cached)
	if len(entries) != 0 {
		t.Errorf("rejected download left behind: %v", entries)
	}
}

// The two worker processes of a CI shard resolve the pin at the same moment,
// so the download has to survive a concurrent copy of itself: every caller
// prints the same path and finds the same verified binary there. The gated
// curl holds every caller inside the download branch until all have arrived,
// so the overlap is certain rather than a matter of scheduling.
func TestPinnedStage0ResolvesConcurrently(t *testing.T) {
	const callers = 6
	root := checkout(t)
	host := hostName(t)
	write(t, filepath.Join(root, "bootstrap", "stage0.lock"), lock(host, sum([]byte(fixpointCompiler))), 0o644)
	bin, urls := gatedCurl(t, []byte(fixpointCompiler), callers)
	cached := filepath.Join(root, "build", "bootstrap", "stage0", "stage0-20260901-0123456")
	want := filepath.Join(cached, "fern-selfhost-"+host) + "\n"

	var wg sync.WaitGroup
	results := make([]string, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command("bash", filepath.Join(root, "bootstrap", "bootstrap.sh"), "stage0")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			var stderr strings.Builder
			cmd.Stderr = &stderr
			got, err := cmd.Output()
			if err != nil {
				errs[i] = fmt.Errorf("caller %d: %v\n%s", i, err, stderr.String())
				return
			}
			results[i] = string(got)
		}(i)
	}
	wg.Wait()
	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Error(errs[i])
			continue
		}
		if results[i] != want {
			t.Errorf("caller %d printed %q, want %q", i, results[i], want)
		}
	}
	if got, err := os.ReadFile(strings.TrimSpace(want)); err != nil || !bytes.Equal(got, []byte(fixpointCompiler)) {
		t.Fatalf("the resolved stage0 is not the released bytes at %s: %v", strings.TrimSpace(want), err)
	}
	entries, _ := os.ReadDir(cached)
	if len(entries) != 1 {
		t.Errorf("scratch files left beside the stage0: %v", entries)
	}
	if asked, _ := os.ReadFile(urls); strings.Count(string(asked), "\n") != callers {
		t.Errorf("not every caller was inside the download branch; curl saw:\n%s", asked)
	}
}

func TestMissingLockAndNoStage0Fails(t *testing.T) {
	root := checkout(t)
	out, err := run(t, root, "build", nil, "")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(out, "no ") || !strings.Contains(out, "STAGE0=") {
		t.Errorf("failure does not explain the two ways to supply a stage0")
	}
}
