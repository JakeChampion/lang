//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

func terminationSignal() os.Signal { return syscall.SIGTERM }

func isolate(cmd *exec.Cmd) {
	if runtime.GOOS == "linux" && cmd.Err == nil {
		cmd.Args = append([]string{"/bin/sh", "-c", `ulimit -c 0 && printf '0\n' > /proc/self/coredump_filter && exec "$@"`, "sh", cmd.Path}, cmd.Args[1:]...)
		cmd.Path = "/bin/sh"
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
}
