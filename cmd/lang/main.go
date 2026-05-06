// Command lang compiles a single .lang source file.
//
// Usage:
//
//	lang FILE.lang                  # write ARM32 assembly to stdout
//	lang -o OUTPUT FILE.lang        # link with the ARM cross-compiler
//	                                # and write a static ELF binary
//	                                # (requires arm-linux-gnueabihf-gcc
//	                                # on PATH, override with -cc PATH)
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

func main() {
	out := flag.String("o", "", "output binary path; if unset, assembly is written to stdout")
	cc := flag.String("cc", "arm-linux-gnueabihf-gcc", "ARM cross-compiler used to link when -o is set")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: lang [-o OUTPUT] [-cc CC] FILE.lang")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	srcPath := flag.Arg(0)

	if err := run(srcPath, *out, *cc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(srcPath, outPath, cc string) error {
	srcBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	src := string(srcBytes)

	prog, err := parser.Parse(src)
	if err != nil {
		return fmt.Errorf("%s", diag.Format(src, err))
	}
	info, err := checker.Check(prog)
	if err != nil {
		return fmt.Errorf("%s", diag.Format(src, err))
	}
	asm, err := codegen.Emit(prog, info)
	if err != nil {
		return err
	}

	if outPath == "" {
		_, err = os.Stdout.WriteString(asm)
		return err
	}
	return link(asm, outPath, cc)
}

// link writes asm to a temp .s file and invokes the cross-compiler to
// produce a static binary at outPath. The temp file is removed on
// success; on failure we leave it in place so the user can inspect it.
func link(asm, outPath, cc string) error {
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("cross-compiler %q not found on PATH (override with -cc): %w", cc, err)
	}
	tmpDir, err := os.MkdirTemp("", "lang-build-*")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(tmpDir)
		}
	}()

	asmPath := filepath.Join(tmpDir, "prog.s")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		return err
	}
	cmd := exec.Command(cc, "-static", asmPath, "-o", outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		keep = true
		return fmt.Errorf("%s failed: %w\n%s\n(temporary assembly retained at %s)", cc, err, out, asmPath)
	}
	return nil
}
