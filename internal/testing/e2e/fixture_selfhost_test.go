package e2e

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/envflag"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestFernFixturesSelfHostWasm(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	wasmtime := e2eharness.Wasmtime(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "wasm",
		knownFile: "selfhost-wasm-known-divergences.txt",
		skipStdin: true,
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			cmd := fixtureCompile(fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", watPath, f.mainPath, stdlibRoot)
			if out, err := cmd.CombinedOutput(); err != nil {
				failf("self-host compile failed: %v\n%s", err, out)
				return
			}
			run := exec.Command(wasmtime, "run", watPath)
			if wat, err := os.ReadFile(watPath); err == nil && bytes.Contains(wat, []byte("wasi:sockets/")) {
				// Sockets are reachable only from a component, which reports
				// just success or failure, so these cases must exit 0.
				compPath := filepath.Join(t.TempDir(), "prog.wasm")
				if out, err := fixtureCompile(fernBin, "-target", "wasm32-wasi", "-o", compPath, f.mainPath, stdlibRoot).CombinedOutput(); err != nil {
					failf("self-host component compile failed: %v\n%s", err, out)
					return
				}
				run = exec.Command(wasmtime, "run", "-S", "inherit-network", compPath)
			}
			checkSelfHostWasmRun(t, f, runSelfHostBin(run, ""), f.wantExit, failf)
		},
	})
}

// The default wasm32-wasi output: a wasi:cli/run component, which reports
// only ok or err, so any nonzero exit reaches the host as 1.
func TestFernFixturesSelfHostWasmComponent(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	wasmtime := e2eharness.Wasmtime(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "wasm_component",
		knownFile: "selfhost-wasm-component-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			compPath := filepath.Join(t.TempDir(), "prog.wasm")
			if out, err := fixtureCompile(fernBin, "-target", "wasm32-wasi", "-o", compPath, f.mainPath, stdlibRoot).CombinedOutput(); err != nil {
				failf("self-host compile failed: %v\n%s", err, out)
				return
			}
			wantExit := 0
			if f.wantExit != 0 {
				wantExit = 1
			}
			r := runSelfHostBin(exec.Command(wasmtime, "run", "-S", "inherit-network", compPath), f.stdin)
			checkSelfHostWasmRun(t, f, r, wantExit, failf)
		},
	})
}

// wasm32-wasi-http accepts only a handler program. This leg serves each
// case that carries expected.http and compares `GET /` with that file.
func TestFernFixturesSelfHostWasiHttp(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	wasmtime := e2eharness.Wasmtime(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "wasi_http",
		knownFile: "selfhost-wasi-http-known-divergences.txt",
		selects:   func(f *fixtureSpec) bool { return f.hasHTTP },
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			compPath := filepath.Join(t.TempDir(), "handler.wasm")
			if out, err := fixtureCompile(fernBin, "-target", "wasm32-wasi-http", "-o", compPath, f.mainPath, stdlibRoot).CombinedOutput(); err != nil {
				failf("self-host compile failed: %v\n%s", err, out)
				return
			}
			status, body, log, err := serveFixtureOnce(t, wasmtime, compPath)
			switch {
			case err != nil:
				failf("GET / failed: %v\nwasmtime serve:\n%s", err, log)
			case strings.Contains(log, "wasm trap:"):
				failf("the handler TRAPPED:\n%s", log)
			case status != f.httpStatus || body != f.httpBody:
				failf("GET / = %d %q, want %d %q\nwasmtime serve:\n%s", status, body, f.httpStatus, f.httpBody, log)
			}
		},
	})
}

func TestFernFixturesSelfHostX86_64(t *testing.T) {
	runSelfHostFixtureLegX86_64(t, nil)
}

func TestFernFixturesSelfHostX86_64Quarantine(t *testing.T) {
	runSelfHostFixtureLegX86_64(t, []string{"FERN_RC_FREE_DEBUG=1"})
}

func runSelfHostFixtureLegX86_64(t *testing.T, env []string) {
	requireSelfHostFixtureLeg(t)
	runner := e2eharness.X86_64Runner(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "x86_64",
		knownFile: "selfhost-x86_64-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath, ok := compileSelfHostNative(t, fernBin, "x86-64-linux", env, f, stdlibRoot, failf)
			if !ok {
				return
			}
			r := runSelfHostBin(e2eharness.RunX86_64Bin(runner, binPath), f.stdin)
			if strings.Contains(r.stderr, "fern-sanitizer:") {
				failf("the sanitizer reported a finding:\n%s", r.stderr)
				return
			}
			if env != nil && f.reclaimObservable {
				exitOnly := *f
				exitOnly.exact, exitOnly.contains = false, nil
				f = &exitOnly
			}
			checkSelfHostNativeRun(t, f, r, failf)
		},
	})
}

func TestFernFixturesSelfHostArm64(t *testing.T) {
	runSelfHostFixtureLegArm64(t, "arm64-linux", "arm64")
}

// arm64-android is a static PIE over the Linux syscalls, so it runs where
// an arm64-linux binary runs.
func TestFernFixturesSelfHostArm64Android(t *testing.T) {
	runSelfHostFixtureLegArm64(t, "arm64-android", "arm64_android")
}

func runSelfHostFixtureLegArm64(t *testing.T, target, backend string) {
	requireSelfHostFixtureLeg(t)
	qemu := e2eharness.Arm64Runner(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   backend,
		knownFile: "selfhost-" + strings.ReplaceAll(backend, "_", "-") + "-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath, ok := compileSelfHostNative(t, fernBin, target, nil, f, stdlibRoot, failf)
			if !ok {
				return
			}
			checkSelfHostNativeRun(t, f, runSelfHostBin(runArm64Bin(qemu, binPath), f.stdin), failf)
		},
	})
}

func TestFernFixturesSelfHostArm64Darwin(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	if e2eharness.HostSelfHostTarget() != e2eharness.TargetArm64Darwin {
		t.Skip("arm64-darwin binaries run only on an Apple Silicon macOS host")
	}
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "arm64_darwin",
		knownFile: "selfhost-arm64-darwin-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			binPath, ok := compileSelfHostNative(t, fernBin, e2eharness.TargetArm64Darwin, nil, f, stdlibRoot, failf)
			if !ok {
				return
			}
			checkSelfHostNativeRun(t, f, runSelfHostBin(exec.Command(binPath), f.stdin), failf)
		},
	})
}

func compileSelfHostNative(t *testing.T, fernBin, target string, env []string, f *fixtureSpec, stdlibRoot string, failf failFunc) (string, bool) {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "prog")
	cmd := fixtureCompile(fernBin, "-target", target, "-o", binPath, f.mainPath, stdlibRoot)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		failf("self-host compile failed: %v\n%s%s", err, out, strictIRBailSite(fernBin, target, nil, env, f.mainPath, stdlibRoot, out))
		return "", false
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return binPath, true
}

func checkSelfHostWasmRun(t *testing.T, f *fixtureSpec, r selfHostRun, wantExit int, failf failFunc) {
	t.Helper()
	out := r.stdout + r.stderr
	rejected, why := wasmRejected(r.stderr)
	switch {
	case r.timedOut:
		failf("the self-host wasm HUNG — killed at %s\nstdout so far: %q\nstderr: %s", selfHostRunTimeout, r.stdout, r.stderr)
	case rejected:
		failf("wasmtime REJECTED the self-host module — not a wrong answer, the artifact is invalid or incomplete (%s):\n%s", why, out)
	case strings.Contains(r.stderr, "wasm trap:"):
		failf("self-host wasm TRAPPED:\n%s", out)
	case wantExit >= 126:
		t.Logf("ran clean; exit value unmeasurable (expected %d, WASI caps at 125)", wantExit)
	default:
		if r.exit != wantExit {
			failf("self-host wasm exit = %d, want %d\n%s", r.exit, wantExit, out)
		}
		checkSelfHostStdout(f, r, failf)
	}
}

func checkSelfHostStdout(f *fixtureSpec, r selfHostRun, failf failFunc) {
	if f.exact {
		if r.stdout != f.wantOut {
			failf("stdout mismatch\n got: %q\nwant: %q\nstderr: %s", r.stdout, f.wantOut, r.stderr)
		}
		return
	}
	for _, sub := range f.contains {
		if !strings.Contains(r.stdout, sub) {
			failf("stdout missing %q\nfull stdout:\n%s\nstderr: %s", sub, r.stdout, r.stderr)
		}
	}
}

func serveFixtureOnce(t *testing.T, wasmtime, component string) (status int, body, log string, err error) {
	t.Helper()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freeLoopbackPort(t)))
	srv := exec.Command(wasmtime, "serve", "--addr", addr, component)
	var slog bytes.Buffer
	srv.Stdout, srv.Stderr = &slog, &slog
	if err := srv.Start(); err != nil {
		return 0, "", "", err
	}
	client := &http.Client{Timeout: selfHostRunTimeout}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		var resp *http.Response
		if resp, err = client.Get("http://" + addr + "/"); err == nil {
			var raw []byte
			raw, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			status, body = resp.StatusCode, string(raw)
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}
	_ = srv.Process.Kill()
	_ = srv.Wait()
	return status, body, slog.String(), err
}

func TestFernFixturesSelfHostInterp(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:   "interp",
		shipped:   true,
		knownFile: "selfhost-interp-known-divergences.txt",
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			cmd := exec.Command(fernBin, "-interp", f.mainPath)
			checkSelfHostNativeRun(t, f, runSelfHostBin(cmd, f.stdin), failf)
		},
	})
}

func TestFernFixturesSelfHostCheck(t *testing.T) {
	requireSelfHostFixtureLeg(t)
	runSelfHostFixtureLeg(t, selfHostLeg{
		backend:    "check",
		knownFile:  "selfhost-check-known-divergences.txt",
		errorCases: true,
		check: func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc) {
			out, err := exec.Command(fernBin, "-check", f.mainPath, stdlibRoot).CombinedOutput()
			if err == nil {
				failf("the self-host front end accepted it; expected an error containing %q\n%s", f.wantErrors[0], out)
				return
			}
			reportDiagnosticMismatches("self-host -check", f.wantErrors, string(out), failf)
		},
	})
}

type failFunc func(format string, args ...any)

type selfHostLeg struct {
	shipped    bool
	backend    string
	knownFile  string
	skipStdin  bool
	errorCases bool
	selects    func(f *fixtureSpec) bool
	check      func(t *testing.T, fernBin, stdlibRoot string, f *fixtureSpec, failf failFunc)
}

func requireSelfHostFixtureLeg(t *testing.T) {
	t.Helper()
	if !envflag.On("FERN_SELFHOST_FIXTURES") {
		t.Skip("set FERN_SELFHOST_FIXTURES=1 to run the fixture corpus through the self-host compiler")
	}
}

func runSelfHostFixtureLeg(t *testing.T, leg selfHostLeg) {
	t.Helper()
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	fernBin := e2eharness.SelfHostCLI(t)
	if leg.shipped {
		fernBin = e2eharness.ShippedFern(t)
	}

	root := conformanceCases
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	known := loadKnownDivergences(t, leg.knownFile)
	var ran, skipped, expectedFail int
	for _, name := range names {
		abs, err := filepath.Abs(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("abs %s: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(abs, "main.fern")); err != nil {
			continue
		}
		f := loadFixture(t, abs)
		selected := !f.rejectionCase() && f.backends[leg.backend] && !(leg.skipStdin && f.stdin != "")
		if leg.errorCases {
			selected = f.compileError
		}
		if selected && leg.selects != nil {
			selected = leg.selects(f)
		}
		if !selected {
			skipped++
			continue
		}
		ran++
		reason, isKnown := known[name]
		if isKnown {
			expectedFail++
		}
		t.Run(name, func(t *testing.T) {
			diverged := false
			failf := func(format string, args ...any) {
				if isKnown {
					diverged = true
					t.Logf("known divergence (%s): "+format, append([]any{reason}, args...)...)
					return
				}
				t.Errorf(format, args...)
			}
			leg.check(t, fernBin, stdlibRoot, f, failf)
			if isKnown && !diverged {
				t.Errorf("listed in %s (%s) but it PASSES now — delete the entry", leg.knownFile, reason)
			}
		})
	}
	skipReasons := "compile-error / " + leg.backend + "-excluded"
	if leg.skipStdin {
		skipReasons += " / stdin"
	}
	if leg.selects != nil {
		skipReasons += " / not selected by this leg"
	}
	if ran == 0 {
		t.Fatalf("the self-host %s leg selected 0 of %d fixtures — the `backend` selector matches "+
			"no fixture's `backends` file, so this leg asserted nothing", leg.backend, len(names))
	}
	t.Logf("self-host %s leg: %d fixtures run (%d of them known divergences), %d skipped (%s)",
		leg.backend, ran, expectedFail, skipped, skipReasons)
}

func fixtureCompile(fernBin string, args ...string) *exec.Cmd {
	return exec.Command(fernBin, args...)
}

func strictIRBailSite(fernBin, target string, emit, env []string, mainPath, stdlibRoot string, firstOut []byte) string {
	if !strings.Contains(string(firstOut), "not IR-eligible") {
		return ""
	}
	args := append([]string{"-target", target}, emit...)
	cmd := exec.Command(fernBin, append(args, "-o", os.DevNull, mainPath, stdlibRoot)...)
	cmd.Env = append(append(os.Environ(), env...), "FERN_STRICT_IR=1")
	out, _ := cmd.CombinedOutput()
	return "\n--- FERN_STRICT_IR=1 ---\n" + string(out)
}

type selfHostRun struct {
	stdout   string
	stderr   string
	exit     int
	exited   bool
	state    string
	elapsed  time.Duration
	timedOut bool
}

const selfHostRunTimeout = 20 * time.Second

func runSelfHostBin(cmd *exec.Cmd, stdin string) selfHostRun {
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	start := time.Now()
	timedOut, err := runBounded(cmd, selfHostRunTimeout)
	if cmd.ProcessState == nil && err != nil {
		return selfHostRun{stderr: err.Error(), exit: -1}
	}
	r := selfHostRun{stdout: so.String(), stderr: se.String(), exit: -1, elapsed: time.Since(start), timedOut: timedOut}
	if ps := cmd.ProcessState; ps != nil {
		r.exit, r.exited, r.state = ps.ExitCode(), ps.Exited(), ps.String()
	}
	return r
}

func checkSelfHostNativeRun(t *testing.T, f *fixtureSpec, r selfHostRun, failf failFunc) {
	t.Helper()
	if r.timedOut {
		failf("the self-host-compiled binary HUNG — killed at %s (natively this fixture finishes in milliseconds)\nstdout so far: %q\nstderr: %s",
			selfHostRunTimeout, r.stdout, r.stderr)
		return
	}
	if !r.exited {
		failf("the self-host-compiled binary CRASHED after %s — %s (not a wrong answer)\nstdout: %q\nstderr: %s",
			r.elapsed.Round(time.Millisecond), r.state, r.stdout, r.stderr)
		return
	}
	if r.exit != f.wantExit {
		hint := ""
		switch r.exit {
		case 125:
			hint = " — 125 is __fern_alloc's arena-exhaustion abort, so this is a leak"
		case 137:
			hint = " — 137 is 128+SIGKILL, i.e. the HOST ran out of RAM, not the arena"
		}
		failf("exit = %d, want %d%s\nstdout: %q\nstderr: %s", r.exit, f.wantExit, hint, r.stdout, r.stderr)
	}
	checkSelfHostStdout(f, r, failf)
}

func loadKnownDivergences(t *testing.T, file string) map[string]string {
	t.Helper()
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		t.Fatalf("read %s: %v", file, err)
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		name, reason, _ := strings.Cut(ln, " ")
		out[strings.TrimSpace(name)] = strings.TrimSpace(reason)
	}
	return out
}
