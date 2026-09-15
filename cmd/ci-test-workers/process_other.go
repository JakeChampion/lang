//go:build !linux && !darwin

package main

import (
	"os"
	"os/exec"
)

func terminationSignal() os.Signal { return os.Interrupt }

// The CI runner is used on Linux. Keep the command buildable on other hosts.
func isolate(cmd *exec.Cmd) {}
