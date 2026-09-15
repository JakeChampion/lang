//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func terminationSignal() os.Signal { return syscall.SIGTERM }

func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
}
