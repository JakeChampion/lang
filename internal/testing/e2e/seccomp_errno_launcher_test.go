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

func failSyscallsWith(nrs []uint32, errno uint32) error {
	var arch uint32
	switch runtime.GOARCH {
	case "amd64":
		arch = 0xC000003E
	case "arm64":
		arch = 0xC00000B7
	default:
		return fmt.Errorf("no audit arch for %s", runtime.GOARCH)
	}
	const (
		ldAbs      = 0x20
		jeqK       = 0x15
		retK       = 0x06
		retAllow   = 0x7fff0000
		retErrno   = 0x00050000
		noNewPrivs = 38
		setSeccomp = 22
		modeFilter = 2
	)
	n := len(nrs)
	prog := []sockFilter{
		{ldAbs, 0, 0, 4},
		{jeqK, 0, uint8(n + 2), arch},
		{ldAbs, 0, 0, 0},
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
