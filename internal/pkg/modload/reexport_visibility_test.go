package modload_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/testing/modulecases"
)

func TestReexportDeclarationsUsePublicNames(t *testing.T) {
	for _, tc := range modulecases.Reexports() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := writeFiles(t, tc.Files)
			prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
			if tc.ErrorFile != "" {
				var coded interface {
					Code() string
					File() string
				}
				if !errors.As(err, &coded) || coded.Code() != "E087" || filepath.Base(coded.File()) != tc.ErrorFile {
					t.Fatalf("got %v, want E087 at %s", err, tc.ErrorFile)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatal(err)
			}
		})
	}
}
