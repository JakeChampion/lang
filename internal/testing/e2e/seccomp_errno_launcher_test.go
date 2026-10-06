//go:build linux

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// Some errnos no unprivileged syscall answers reliably, so a test that needs
// one re-executes its own binary as a launcher that installs a seccomp filter
// failing chosen syscalls with it, then execs the program under test. The
// filter is on the host's own syscall numbers, so it holds for a binary run
// under qemu-user too: the emulator issues the host syscall on the guest's
// behalf.
//
// The variable carries "ERRNO NR[,NR...] DIR".
const seccompLauncherEnv = "FERN_TEST_SECCOMP_ERRNO"

func init() {
	spec := os.Getenv(seccompLauncherEnv)
	if spec == "" {
		return
	}
	if err := launchUnderSeccomp(spec); err != nil {
		fmt.Fprintln(os.Stderr, "seccomp launcher:", err)
		os.Exit(125)
	}
}

func launchUnderSeccomp(spec string) error {
	fields := strings.SplitN(spec, " ", 3)
	if len(fields) != 3 {
		return fmt.Errorf("malformed %s=%q", seccompLauncherEnv, spec)
	}
	errno, err := strconv.ParseUint(fields[0], 10, 16)
	if err != nil {
		return err
	}
	var nrs []uint32
	for _, f := range strings.Split(fields[1], ",") {
		nr, err := strconv.ParseUint(f, 10, 32)
		if err != nil {
			return err
		}
		nrs = append(nrs, uint32(nr))
	}
	if err := os.Chdir(fields[2]); err != nil {
		return err
	}
	if err := failSyscallsWith(nrs, uint32(errno)); err != nil {
		return err
	}
	path, err := exec.LookPath(os.Args[1])
	if err != nil {
		return err
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, seccompLauncherEnv+"=") {
			env = append(env, kv)
		}
	}
	return fmt.Errorf("exec: %w", syscall.Exec(path, os.Args[1:], env))
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

// failSyscallsWith installs, on the calling thread, a filter that answers
// each of nrs with errno and allows everything else. An exec from the same
// thread keeps it.
func failSyscallsWith(nrs []uint32, errno uint32) error {
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
	n := len(nrs)
	prog := []sockFilter{
		{ldAbs, 0, 0, 4}, // seccomp_data.arch
		{jeqK, 0, uint8(n + 2), arch},
		{ldAbs, 0, 0, 0}, // seccomp_data.nr
	}
	for i, nr := range nrs {
		var jf uint8
		if i == n-1 {
			jf = 1
		}
		prog = append(prog, sockFilter{jeqK, uint8(n - 1 - i), jf, nr})
	}
	prog = append(prog,
		sockFilter{retK, 0, 0, retErrno | errno},
		sockFilter{retK, 0, 0, retAllow},
	)
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

// underSeccompErrno wraps argv in the launcher, which execs it in dir with
// each of nrs failing with errno. The launcher itself starts here: the
// package's other initialisers find the repository from the working
// directory.
func underSeccompErrno(t *testing.T, dir string, errno syscall.Errno, nrs []uintptr, argv ...string) *exec.Cmd {
	t.Helper()
	list := make([]string, len(nrs))
	for i, nr := range nrs {
		list[i] = strconv.FormatUint(uint64(nr), 10)
	}
	cmd := exec.Command(os.Args[0], argv...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%d %s %s", seccompLauncherEnv, errno, strings.Join(list, ","), dir))
	return cmd
}
