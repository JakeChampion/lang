package main

import (
	"fmt"
	"os"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/parser"
)

func main() {
	src, _ := os.ReadFile(os.Args[1])
	prog, err := parser.Parse(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		fmt.Fprintln(os.Stderr, "constfold:", err)
		os.Exit(1)
	}
	info, err := checker.Check(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check:", err)
		os.Exit(1)
	}
	asm, err := arm64.Emit(prog, info)
	if err != nil {
		fmt.Fprintln(os.Stderr, "emit:", err)
		os.Exit(1)
	}
	fmt.Print(asm)
}
