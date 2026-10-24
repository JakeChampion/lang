package component_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
	"github.com/jakechampion/lang/internal/wasm/leb128"
)

func coreModuleWithImports(pairs [][2]string) []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

	out = append(out, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00)

	var imports []byte
	imports = leb128.UlebU64(imports, uint64(len(pairs)))
	for _, p := range pairs {
		imports = leb128.UlebU64(imports, uint64(len(p[0])))
		imports = append(imports, p[0]...)
		imports = leb128.UlebU64(imports, uint64(len(p[1])))
		imports = append(imports, p[1]...)
		imports = append(imports, 0x00, 0x00)
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
		{"wasi:filesystem/types@0.2.0", "[method]descriptor.stat"},
	}))
	if len(unsupported) != 0 {
		t.Errorf("unsupported = %q, want none", unsupported)
	}
	if !req.File.Stat || !req.File.StatSelf || req.File.OpenAt {
		t.Errorf("File = %+v, want Stat and StatSelf set and OpenAt clear", req.File)
	}
}

func TestClassifyCoreAcceptsErrorDropAlone(t *testing.T) {
	req, unsupported := component.ClassifyCore(coreModuleWithImports([][2]string{
		{"wasi:io/error@0.2.0", "[resource-drop]error"},
	}))
	if len(unsupported) != 0 || !req.DropError || component.RequestEmpty(req) {
		t.Fatalf("owned error drop must compose on its own: request=%+v unsupported=%v", req, unsupported)
	}
}

func TestClassifyCoreRejectsIncompleteFilesystemChain(t *testing.T) {
	_, unsupported := component.ClassifyCore(coreModuleWithImports([][2]string{
		{"wasi:filesystem/preopens@0.2.0", "get-directories"},
	}))
	if len(unsupported) == 0 {
		t.Error("get-directories without any method accepted, want it reported")
	}
}

func TestClassifyCoreAcceptsHandleMethodAlone(t *testing.T) {
	req, unsupported := component.ClassifyCore(coreModuleWithImports([][2]string{
		{"wasi:filesystem/types@0.2.0", "[method]descriptor.stat"},
	}))
	if len(unsupported) != 0 {
		t.Errorf("unsupported = %q, want none", unsupported)
	}
	if !req.File.StatSelf {
		t.Errorf("File = %+v, want StatSelf set", req.File)
	}
}
