package sourcelint

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
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
