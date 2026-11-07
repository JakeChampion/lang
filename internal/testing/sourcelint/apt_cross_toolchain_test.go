package sourcelint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCrossToolchainRequiresRequestedEmulator(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actions", "apt-cross-toolchain", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	predicate := regexp.MustCompile(`(?ms)^        toolchain_present\(\) \{\n.*?^        \}`).Find(src)
	if len(predicate) == 0 {
		t.Fatal("action readiness predicate not found")
	}
	for _, tc := range []struct {
		name, pkg, gcc, present string
		libc, headers, want     bool
	}{
		{"regular-not-static", "qemu-user", "true", "aarch64-linux-gnu-gcc aarch64-linux-gnu-as qemu-aarch64-static", false, false, false},
		{"static-not-regular", "qemu-user-static", "true", "aarch64-linux-gnu-gcc aarch64-linux-gnu-as qemu-aarch64", false, false, false},
		{"regular-ready", "qemu-user", "true", "aarch64-linux-gnu-gcc aarch64-linux-gnu-as qemu-aarch64", false, false, true},
		{"static-ready", "qemu-user-static", "true", "aarch64-linux-gnu-gcc aarch64-linux-gnu-as qemu-aarch64-static", false, false, true},
		{"libc-ready", "qemu-user", "true", "aarch64-linux-gnu-gcc aarch64-linux-gnu-as qemu-aarch64", true, true, true},
		{"headers-missing", "qemu-user", "true", "aarch64-linux-gnu-gcc aarch64-linux-gnu-as qemu-aarch64", true, false, false},
		{"assembler-missing", "qemu-user", "true", "aarch64-linux-gnu-gcc qemu-aarch64", false, false, false},
		{"compiler-missing", "qemu-user", "true", "aarch64-linux-gnu-as qemu-aarch64", false, false, false},
		{"emulator-only", "qemu-user", "false", "qemu-aarch64", false, false, true},
		{"static-only", "qemu-user-static", "false", "qemu-aarch64-static", false, false, true},
		{"system-only", "qemu-system-arm", "false", "qemu-system-aarch64", false, false, true},
		{"system-not-user", "qemu-system-arm", "false", "qemu-aarch64 qemu-aarch64-static", false, false, false},
		{"nothing-installed", "qemu-user", "false", "", false, false, false},
		{"unknown-package", "unknown", "false", "qemu-aarch64 qemu-aarch64-static", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := string(predicate) + `
command() {
  [ "$1" = "-v" ] || return 2
  case " $PRESENT " in
    *" $2 "*) return 0 ;;
    *) return 1 ;;
  esac
}
aarch64-linux-gnu-gcc() {
  [ "$HEADERS_READY" = "true" ]
}
toolchain_present
`
			cmd := exec.Command(bash, "-c", script)
			cmd.Env = []string{"QEMU_PKG=" + tc.pkg, "WANT_GCC=" + tc.gcc, "WANT_LIBC=" + strconv.FormatBool(tc.libc), "PRESENT=" + tc.present, "HEADERS_READY=" + strconv.FormatBool(tc.headers)}
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.want {
				t.Fatalf("ready=%v, want %v: %v\n%s", err == nil, tc.want, err, out)
			}
			if err != nil {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("predicate did not return an ordinary refusal: %v\n%s", err, out)
				}
			}
		})
	}
}

// The version check after installation must run the emulator that the
// requested package installs: qemu-system-arm has no qemu-aarch64.
func TestCrossToolchainReportsTheRequestedEmulator(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actions", "apt-cross-toolchain", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	report := regexp.MustCompile(`(?ms)^        case "\$QEMU_PKG" in\n.*?--version.*?^        esac`).Find(src)
	if len(report) == 0 {
		t.Fatal("emulator version check not found")
	}
	for pkg, want := range map[string]string{
		"qemu-user":        "qemu-aarch64",
		"qemu-user-static": "qemu-aarch64-static",
		"qemu-system-arm":  "qemu-system-aarch64",
	} {
		t.Run(pkg, func(t *testing.T) {
			script := `
qemu-aarch64() { echo qemu-aarch64; }
qemu-aarch64-static() { echo qemu-aarch64-static; }
qemu-system-aarch64() { echo qemu-system-aarch64; }
set -e
` + string(report)
			cmd := exec.Command(bash, "-c", script)
			cmd.Env = []string{"QEMU_PKG=" + pkg, "PATH=" + os.Getenv("PATH")}
			out, err := cmd.CombinedOutput()
			if err != nil || strings.TrimSpace(string(out)) != want {
				t.Fatalf("ran %q, want %q: %v", strings.TrimSpace(string(out)), want, err)
			}
		})
	}
}

func TestCrossToolchainSharesInstallDeadline(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actions", "apt-cross-toolchain", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(src), "        # Limit package setup")
	if !ok {
		t.Fatal("package setup script not found")
	}
	body = "# Limit package setup" + strings.ReplaceAll(body, "\n        ", "\n")
	for _, tc := range []struct {
		name, scenario, want string
		cached               bool
		exit                 int
	}{
		{"cache-hit", "cache-hit", "120 dpkg\n", true, 0},
		{"retry-recovers", "retry-recovers", "300 apt-get update\n300 apt-get update\n420 apt-get install\n", false, 0},
		{"updates-exhaust-budget", "slow-update", "300 apt-get update\n285 apt-get update\n", false, 1},
		{"install-exhausts-budget", "slow-install", "300 apt-get update\n420 apt-get install\n165 apt-get update\n", false, 1},
		{"cache-shares-budget", "slow-cache", "120 dpkg\n300 apt-get update\n165 apt-get update\n", true, 1},
		{"four-fast-failures", "fast-failure", "300 apt-get update\n300 apt-get update\n300 apt-get update\n300 apt-get update\n", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.cached {
				if err := os.WriteFile(filepath.Join(dir, "cached.deb"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate elapsed seconds without network access or root privileges.
			// An unset SECONDS loses its special property, so real time does
			// not advance it.
			mocks := `
unset SECONDS
SECONDS=0
ready=0
calls=0
sudo() {
  local privileged=1
  case "$1" in
    timeout) "$@" ;;
    mkdir|chown|rm) return 0 ;;
    *) return 90 ;;
  esac
}
timeout() {
  [ "${privileged:-0}" = 1 ] && [ "$1" = --kill-after=10 ] || return 91
  local limit="$2" tool="$3" operation="${4:-}"
  if [ "$tool" = dpkg ]; then
    echo "$limit dpkg" >> "$TRACE"
    if [ "$SCENARIO" = cache-hit ]; then ready=1; return 0; fi
  else
    echo "$limit $tool $operation" >> "$TRACE"
    calls=$((calls + 1))
    if [ "$SCENARIO" = retry-recovers ]; then
      if [ "$calls" = 1 ]; then return 100; fi
      if [ "$operation" = install ]; then ready=1; fi
      return 0
    fi
    if [ "$SCENARIO" = fast-failure ]; then return 100; fi
    if [ "$SCENARIO" = slow-install ] && [ "$calls" = 1 ]; then return 0; fi
  fi
  SECONDS=$((SECONDS + limit))
  return 124
}
sleep() { SECONDS=$((SECONDS + $1)); }
command() { [ "$ready" = 1 ]; }
aarch64-linux-gnu-gcc() { echo compiler; }
qemu-aarch64() { echo emulator; }
`
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", "-c", mocks+body)
			trace := filepath.Join(dir, "trace")
			cmd.Env = ciEnv("CACHE_DIR="+dir, "WANT_GCC=true", "WANT_LIBC=false", "QEMU_PKG=qemu-user", "SCENARIO="+tc.scenario, "TRACE="+trace)
			out, err := cmd.CombinedOutput()
			exit := 0
			if err != nil {
				failure, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("script did not exit: %v\n%s", err, out)
				}
				exit = failure.ExitCode()
			}
			if exit != tc.exit {
				t.Fatalf("exit=%d, want %d: %s", exit, tc.exit, out)
			}
			got, err := os.ReadFile(trace)
			if err != nil || string(got) != tc.want {
				t.Fatalf("command trace=%q, want %q: %v\n%s", got, tc.want, err, out)
			}
		})
	}
}

func TestCrossToolchainKillsUnresponsiveInstall(t *testing.T) {
	timeout, err := exec.LookPath("timeout")
	if err != nil {
		t.Skip("GNU timeout unavailable")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "actions", "apt-cross-toolchain", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	helper := regexp.MustCompile(`(?ms)^        privileged_run\(\) \{\n.*?^        \}`).Find(src)
	if len(helper) == 0 {
		t.Fatal("package timeout helper not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	script := string(helper) + `
sudo() { local privileged=1; "$@"; }
timeout() {
  [ "${privileged:-0}" = 1 ] || return 91
  "$REAL_TIMEOUT" "$@"
}
deadline=$((SECONDS + 1))
privileged_run 300 bash -c 'trap "" TERM; echo started; while :; do sleep 1; done'
status=$?
[ "$status" = 137 ] || exit 92
deadline=$SECONDS
privileged_run 300 echo must-not-run
[ "$?" = 124 ]
`
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = ciEnv("REAL_TIMEOUT=" + timeout)
	out, err := cmd.CombinedOutput()
	if err != nil || ctx.Err() != nil || !strings.Contains(string(out), "started") || strings.Contains(string(out), "must-not-run") {
		t.Fatalf("timeout did not kill the unresponsive install or enforce the deadline: %v\n%s", err, out)
	}
}
