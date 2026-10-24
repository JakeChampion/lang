package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/modulecases"
)

func TestSelfHostReexportDeclarations(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range modulecases.Reexports() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			for name, source := range tc.Files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			entry := filepath.Join(dir, "main.fern")
			cmd := runX86_64Bin(cli.runner, cli.bin, "-check", entry, cli.stdlib)
			output, err := cmd.CombinedOutput()
			if tc.ErrorFile != "" {
				if err == nil || !strings.Contains(string(output), "error[E087]") || !strings.Contains(string(output), tc.ErrorFile) {
					t.Fatalf("got %v\n%s, want E087", err, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("check: %v\n%s", err, output)
			}
			cmd = runX86_64Bin(cli.runner, cli.bin, "-interp", entry, cli.stdlib)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("interpreter: %v\n%s", err, output)
			}
		})
	}
}
