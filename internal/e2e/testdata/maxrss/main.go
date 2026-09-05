// Command maxrss runs its arguments as a program and prints that program's
// peak resident set size in KiB on stdout, exiting with the program's exit
// code, or 255 when a signal killed it. A child's rusage high-water mark
// starts at the RSS of the process that forked it, so a Go test binary reads
// its own footprint back for any child smaller than itself; this program is
// small enough to measure one.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: maxrss prog [args...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState == nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		fmt.Fprintln(os.Stderr, "no rusage for the child")
		os.Exit(2)
	}
	fmt.Println(ru.Maxrss)
	code := cmd.ProcessState.ExitCode()
	if code < 0 {
		fmt.Fprintln(os.Stderr, cmd.ProcessState)
		code = 255
	}
	os.Exit(code)
}
