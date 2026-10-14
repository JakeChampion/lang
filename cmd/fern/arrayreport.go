package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jakechampion/lang/internal/tools/launcher"
)

// runArrayReport implements `fern -array-report FILE.fern`: compile the entry
// with the self-host compiler under FERN_ARRAY_REPORT=1 and print the array
// decisions its rewriting passes report — every std/array chain's fusion
// verdict, the map storage and ndarray kernel decisions, each with its closed
// reason tag (docs/ARRAY-ALGEBRA.md). The compile targets arm64-linux, the
// default target, and its output is discarded.
func runArrayReport(srcPath string, w io.Writer) error {
	compiler, err := launcher.Compiler()
	if err != nil {
		return err
	}
	stdlib, err := launcher.StdlibRoot()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "fern-array-report-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command(compiler, "-target", "arm64-linux", "-o", filepath.Join(dir, "prog"), srcPath, stdlib)
	cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT=1")
	var report bytes.Buffer
	cmd.Stderr = &report
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s", bytes.TrimRight(report.Bytes(), "\n"))
		}
		return err
	}
	_, err = w.Write(report.Bytes())
	return err
}
