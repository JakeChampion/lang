// Command lang compiles a single .lang source file to ARM 32-bit assembly
// on stdout.
//
//	lang path/to/program.lang > program.s
package main

import (
	"fmt"
	"os"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: lang <source.lang>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	srcStr := string(src)

	prog, err := parser.Parse(srcStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(srcStr, err))
		os.Exit(1)
	}
	info, err := checker.Check(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(srcStr, err))
		os.Exit(1)
	}
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.WriteString(asm); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
