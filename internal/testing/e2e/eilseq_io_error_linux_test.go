//go:build linux

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// EILSEQ is IoError.InvalidUtf8 on every target (#11707). No unprivileged
// syscall reliably answers EILSEQ, so the test binary re-executes itself as a
// launcher that installs a seccomp filter failing unlinkat with it, then execs
// the program under test. The filter is on the host's own syscall numbers, so
// it holds for a binary run under qemu-user too: the emulator issues the host
// unlinkat on the guest's behalf.
const eilseqLauncherEnv = "FERN_TEST_EILSEQ_UNLINKAT"

func init() {
	dir := os.Getenv(eilseqLauncherEnv)
	if dir == "" {
		return
	}
	if err := os.Chdir(dir); err != nil {
		fmt.Fprintln(os.Stderr, "eilseq launcher:", err)
		os.Exit(125)
	}
	if err := failUnlinkatWithEilseq(); err != nil {
		fmt.Fprintln(os.Stderr, "eilseq launcher:", err)
		os.Exit(125)
	}
	path, err := exec.LookPath(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "eilseq launcher:", err)
		os.Exit(125)
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, eilseqLauncherEnv+"=") {
			env = append(env, kv)
		}
	}
	err = syscall.Exec(path, os.Args[1:], env)
	fmt.Fprintln(os.Stderr, "eilseq launcher: exec:", err)
	os.Exit(125)
}

type sockFilter struct {
	code   uint16
	jt, jf uint8
	k      uint32
}

type sockFprog struct {
	len    uint16
	filter *sockFilter
}

// failUnlinkatWithEilseq installs, on the calling thread, a filter that
// answers unlinkat with EILSEQ and allows everything else. An exec from the
// same thread keeps it.
func failUnlinkatWithEilseq() error {
	var arch uint32
	switch runtime.GOARCH {
	case "amd64":
		arch = 0xC000003E // AUDIT_ARCH_X86_64
	case "arm64":
		arch = 0xC00000B7 // AUDIT_ARCH_AARCH64
	default:
		return fmt.Errorf("no audit arch for %s", runtime.GOARCH)
	}
	const (
		ldAbs      = 0x20 // BPF_LD | BPF_W | BPF_ABS
		jeqK       = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
		retK       = 0x06 // BPF_RET | BPF_K
		retAllow   = 0x7fff0000
		retErrno   = 0x00050000
		noNewPrivs = 38 // PR_SET_NO_NEW_PRIVS
		setSeccomp = 22 // PR_SET_SECCOMP
		modeFilter = 2  // SECCOMP_MODE_FILTER
	)
	prog := []sockFilter{
		{ldAbs, 0, 0, 4}, // seccomp_data.arch
		{jeqK, 0, 3, arch},
		{ldAbs, 0, 0, 0}, // seccomp_data.nr
		{jeqK, 0, 1, syscall.SYS_UNLINKAT},
		{retK, 0, 0, retErrno | uint32(syscall.EILSEQ)},
		{retK, 0, 0, retAllow},
	}
	fprog := sockFprog{len: uint16(len(prog)), filter: &prog[0]}
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, noNewPrivs, 1, 0); e != 0 {
		return fmt.Errorf("PR_SET_NO_NEW_PRIVS: %v", e)
	}
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, setSeccomp, modeFilter, uintptr(unsafe.Pointer(&fprog))); e != 0 {
		return fmt.Errorf("PR_SET_SECCOMP: %v", e)
	}
	runtime.KeepAlive(prog)
	return nil
}

// underEilseqUnlinkat wraps argv in the launcher, which execs it in dir. The
// launcher itself starts here: the package's other initialisers find the
// repository from the working directory.
func underEilseqUnlinkat(dir string, argv ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], argv...)
	cmd.Env = append(os.Environ(), eilseqLauncherEnv+"="+dir)
	return cmd
}

// The path is relative because wasm resolves every path against its preopen.
const eilseqProg = `function main(): i32 {
    match (remove_file("victim.txt")) {
        Ok(_) => { print("removed"); return 1; },
        Err(e) => {
            match (e) {
                InvalidUtf8(p) => { print("InvalidUtf8 " + p); return 0; },
                Other(_, m) => { print("Other " + m); return 2; },
                _ => { print("another variant"); return 3; }
            }
        }
    }
}
`

func TestEilseqIsInvalidUtf8EveryTarget(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.txt")
	src := eilseqProg
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "InvalidUtf8 victim.txt"

	targets := []struct {
		name string
		argv func(t *testing.T) []string
	}{
		{"interpreter", func(t *testing.T) []string {
			return []string{e2eharness.BuildLangBinForInterp(t), "-interp", srcPath}
		}},
		{"x86_64", func(t *testing.T) []string {
			bin, runner := e2eharness.CompileX86_64Bin(t, src)
			return e2eharness.RunX86_64Bin(runner, bin).Args
		}},
		{"arm64", func(t *testing.T) []string {
			bin, qemu := e2eharness.CompileArm64Bin(t, src)
			return e2eharness.RunArm64Bin(qemu, bin).Args
		}},
		{"wasm32-wasi", func(t *testing.T) []string {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Fatal("wasmtime not on PATH")
			}
			wasm := filepath.Join(t.TempDir(), "main.wasm")
			fern := e2eharness.BuildLangBinForInterp(t)
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-o", wasm, srcPath).CombinedOutput(); err != nil {
				t.Fatalf("fern -target wasm32-wasi: %v\n%s", err, out)
			}
			return []string{"wasmtime", "run", "--dir", dir, wasm}
		}},
		{"wasm-core", func(t *testing.T) []string {
			core := e2eharness.CompileSelfHostSource(t, e2eharness.TargetWasm32Wasi, src, nil)
			return []string{"wasmtime", "run", "--dir", dir, core}
		}},
		{"wasm-component", func(t *testing.T) []string {
			return []string{"wasmtime", "run", "--dir=" + dir, buildComponent(t, src)}
		}},
	}
	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := underEilseqUnlinkat(dir, tc.argv(t)...).CombinedOutput()
			if err != nil || !strings.Contains(string(out), want) {
				t.Errorf("%v\noutput %q, want it to contain %q", err, out, want)
			}
			if _, err := os.Stat(victim); err != nil {
				t.Errorf("the filter did not hold: %v", err)
			}
		})
	}
}
