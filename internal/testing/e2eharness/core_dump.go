package e2eharness

import (
	"os/exec"
	"runtime"
)

// WithoutCoreDump runs cmd with a Linux coredump_filter of 0, so an expected
// crash dumps no memory mappings. A piped core handler ignores RLIMIT_CORE,
// and a reserved runtime arena otherwise streams gigabytes to it.
func WithoutCoreDump(cmd *exec.Cmd) *exec.Cmd {
	if runtime.GOOS != "linux" || cmd.Err != nil {
		return cmd
	}
	args := append([]string{"-c", `printf '0\n' > /proc/self/coredump_filter && exec "$@"`, "sh", cmd.Path}, cmd.Args[1:]...)
	out := exec.Command("/bin/sh", args...)
	out.Env, out.Dir = cmd.Env, cmd.Dir
	return out
}
