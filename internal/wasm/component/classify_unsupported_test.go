package component_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

// ClassifyCore's rejection path: an import the composer cannot place
// must come back in `unsupported` so the driver can say so, rather than
// being dropped on the floor and producing a component that fails to
// instantiate later.
//
// This used to be covered end to end, by compiling a Fern program whose
// builtins had no preview-2 route. That is no longer possible: with
// #6208 closed, every filesystem builtin composes, and the source those
// tests used had already had to move three times as the composer grew
// (print → read+append → stat → read_dir). A test whose subject keeps
// disappearing is testing the to-do list, not the contract — so the
// contract moved here, where an unknown import can simply be handed to
// the classifier.

// coreModuleWithImports builds a minimal core wasm module declaring one
// `() -> ()` functype and importing the given (module, name) pairs
// against it. Enough for ClassifyCore, which only walks the import
// section.
func coreModuleWithImports(pairs [][2]string) []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	// Type section: one func type, no params, no results.
	out = append(out, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00)

	var imports []byte
	imports = leb128.UlebU64(imports, uint64(len(pairs)))
	for _, p := range pairs {
		imports = leb128.UlebU64(imports, uint64(len(p[0])))
		imports = append(imports, p[0]...)
		imports = leb128.UlebU64(imports, uint64(len(p[1])))
		imports = append(imports, p[1]...)
		imports = append(imports, 0x00, 0x00) // func, typeidx 0
	}
	out = append(out, 0x02)
	out = leb128.UlebU64(out, uint64(len(imports)))
	return append(out, imports...)
}

func TestClassifyCoreReportsUnknownImport(t *testing.T) {
	_, unsupported := component.ClassifyCore(coreModuleWithImports([][2]string{
		{"wasi:cli/stdout@0.2.0", "get-stdout"},
		{"wasi_snapshot_preview1", "sock_accept"},
	}))
	if len(unsupported) != 1 || unsupported[0] != "wasi_snapshot_preview1.sock_accept" {
		t.Errorf("unsupported = %q, want exactly the one unknown import", unsupported)
	}
}

func TestClassifyCoreAcceptsKnownImports(t *testing.T) {
	req, unsupported := component.ClassifyCore(coreModuleWithImports([][2]string{
		{"wasi:filesystem/preopens@0.2.0", "get-directories"},
		{"wasi:filesystem/types@0.2.0", "[method]descriptor.stat-at"},
	}))
	if len(unsupported) != 0 {
		t.Errorf("unsupported = %q, want none", unsupported)
	}
	if !req.File.Stat || req.File.OpenAt {
		t.Errorf("File = %+v, want Stat set and OpenAt clear", req.File)
	}
}

// A descriptor method without get-directories cannot work: every one of
// them resolves a preopen first. get-directories on its own is equally
// incomplete — nothing would use the descriptor.
func TestClassifyCoreRejectsIncompleteFilesystemChain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pairs [][2]string
	}{
		{"method without get-directories", [][2]string{
			{"wasi:filesystem/types@0.2.0", "[method]descriptor.unlink-file-at"},
		}},
		{"get-directories without any method", [][2]string{
			{"wasi:filesystem/preopens@0.2.0", "get-directories"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, unsupported := component.ClassifyCore(coreModuleWithImports(tc.pairs))
			if len(unsupported) == 0 {
				t.Error("incomplete filesystem chain accepted, want it reported")
			}
		})
	}
}
