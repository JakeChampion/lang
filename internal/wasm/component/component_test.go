package component_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// TestPutTypeSectionInstanceWithInnerTypes_ResultParam round-trips
// an instance-type that declares an inner `result<_, _>` type and
// references it from the function's `status` parameter — the
// shape `wasi:cli/exit@0.2.0::exit` expects. The component is
// otherwise empty (no module / imports), so this isolates the
// type-section encoding.
func TestPutTypeSectionInstanceWithInnerTypes_ResultParam(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionInstanceWithInnerTypesAndOneFuncNoResultExport(
		buf,
		[][]byte{component.InnerTypeResultEmpty},
		"exit",
		[]string{"status"},
		[]byte{0x00}, // inner-typeidx 0
	)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"(result)",
		"\"exit\"",
		"\"status\"",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed output, got:\n%s", want, out)
		}
	}
}

// TestPutComponentTypeSection_StructuralRoundTrip emits a component
// with just a header + a synthetic component-type custom section
// payload and confirms wasm-tools accepts it + surfaces the
// section under that name.
func TestPutComponentTypeSection_StructuralRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	payload := []byte{'t', 'e', 's', 't'}
	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentTypeSection(buf, payload)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("component-type")) {
		t.Errorf("expected `component-type` in printed output, got:\n%s", out)
	}
}

// TestPutCanonSectionLowerWithMemory_Bytes pins the bytes of a
// canon-lower entry carrying a single `memory` canonical-ABI
// option. The expected bytes match what wasm-tools emits for the
// wasi:io/streams::blocking-write-and-flush lowering: it carries
// a list<u8> param so canon-lower needs memory to know where the
// (ptr, len) point.
//
// Wire shape:
//
//	08 07       -- section id 8 (canon), body size 7
//	01          -- vec(1) canons
//	01 00 01    -- canon-lower, function sub-tag, funcIdx 1
//	01          -- opts vec(1)
//	03 00       -- canonopt: memory (0x03), memidx 0
func TestPutCanonSectionLowerWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerWithMemory(nil, 1, 0)
	want := []byte{0x08, 0x07, 0x01, 0x01, 0x00, 0x01, 0x01, 0x03, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerWithMemory(1, 0) = % x, want % x", got, want)
	}
}

// TestPutCanonSectionLowerWithMemoryRealloc_Bytes pins the bytes
// of a canon-lower entry carrying both memory + realloc opts.
// Expected shape:
//
//	08 09       -- section id 8 (canon), body size 9
//	01          -- vec(1) canons
//	01 00 02    -- canon-lower, function sub-tag, funcIdx 2
//	02          -- opts vec(2)
//	03 00       -- canonopt: memory (0x03), memidx 0
//	04 05       -- canonopt: realloc (0x04), funcidx 5
func TestPutCanonSectionLowerWithMemoryRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerWithMemoryRealloc(nil, 2, 0, 5)
	want := []byte{0x08, 0x09, 0x01, 0x01, 0x00, 0x02, 0x02, 0x03, 0x00, 0x04, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerWithMemoryRealloc(2, 0, 5) = % x, want % x", got, want)
	}
}

// TestPutAliasSectionInstanceExportType_Bytes pins the bytes of
// a top-level alias that exposes a type exported by a component
// instance. The expected bytes match what wasm-tools emits for
// the wasi:io/streams component's `alias export 0 "error"` —
// the alias that surfaces wasi:io/error's `error` resource at
// the top-level type space.
//
// Wire shape:
//
//	06 0a                   -- section id 6 (alias), body size 10
//	01                      -- vec(1) aliases
//	03                      -- sort = type
//	00                      -- target: from-instance-export
//	00                      -- instance idx 0
//	05 "error"              -- name (uleb 5 + 5 bytes)
func TestPutAliasSectionInstanceExportType_Bytes(t *testing.T) {
	got := component.PutAliasSectionInstanceExportType(nil, 0, "error")
	want := []byte{0x06, 0x0a, 0x01, 0x03, 0x00, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r'}
	if !bytes.Equal(got, want) {
		t.Errorf("PutAliasSectionInstanceExportType(0, %q) = % x, want % x", "error", got, want)
	}
}

// TestOuterAliasTypeDecl_Bytes pins the 5-byte outer-alias decl
// emitted inside instance-type bodies. The example matches what
// wasm-tools emits inside the wasi:io/streams instance type:
//   `(alias outer 1 1 (type (;1;)))`
// — 1 scope up, outer typeidx 1.
func TestOuterAliasTypeDecl_Bytes(t *testing.T) {
	got := component.OuterAliasTypeDecl(1, 1)
	want := []byte{0x00, 0x03, 0x02, 0x01, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("OuterAliasTypeDecl(1, 1) = % x, want % x", got, want)
	}
}

// TestExportSubResourceDecl_Bytes pins the bytes for a
// `resource error;` declaration inside an instance type body —
// the shape used by wasi:io/error.
func TestExportSubResourceDecl_Bytes(t *testing.T) {
	got := component.ExportSubResourceDecl("error")
	want := []byte{0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("ExportSubResourceDecl(%q) = % x, want % x", "error", got, want)
	}
}

// TestExportTypeEqDecl_Bytes pins the bytes for an `export
// "error" (type (eq 1))` decl — the shape wasi:io/streams uses
// to re-export the outer-aliased error type as its own typeidx.
func TestExportTypeEqDecl_Bytes(t *testing.T) {
	got := component.ExportTypeEqDecl("error", 1)
	want := []byte{0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("ExportTypeEqDecl(%q, 1) = % x, want % x", "error", got, want)
	}
}

// TestWasiIoErrorInstanceTypeBody_Composed exercises the
// pieces together by composing the wasi:io/error instance type
// body and validating it via the escape hatch. The body is
// just `vec(1) decls + ExportSubResourceDecl("error")` plus
// the standard instance-type framing.
func TestWasiIoErrorInstanceTypeBody_Composed(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	resourceDecl := component.ExportSubResourceDecl("error")
	body := []byte{0x01, 0x42, 0x01}
	body = append(body, resourceDecl...)
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, body)
	dir := t.TempDir()
	compPath := filepath.Join(dir, "iee.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"\"error\"", "(sub resource)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
}

// TestWasiIoErrorInstanceTypeBody_Bytes pins the exact bytes of
// the wasi:io/error instance type body. Matches what wasm-tools
// emits for the canonical `interface error { resource error; }`.
func TestWasiIoErrorInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiIoErrorInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x01,
		0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoErrorInstanceTypeBody() = % x, want % x", got, want)
	}
}
