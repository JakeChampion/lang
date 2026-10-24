//go:build !linux && !darwin

package main

import (
	"os"
	"os/exec"
)

func terminationSignal() os.Signal { return os.Interrupt }

func isolate(cmd *exec.Cmd) {}
