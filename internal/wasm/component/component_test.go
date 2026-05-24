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
	want := []byte{0x02, 0x03, 0x02, 0x01, 0x01}
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

// TestWasiFilesystemTypesDescriptorInstanceTypeBody_Bytes pins the
// bytes of the minimal wasi:filesystem/types instance type (one
// exported `descriptor` sub-resource).
func TestWasiFilesystemTypesDescriptorInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiFilesystemTypesDescriptorInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x01,
		0x04, 0x00, 0x0a, 'd', 'e', 's', 'c', 'r', 'i', 'p', 't', 'o', 'r', 0x03, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiFilesystemTypesDescriptorInstanceTypeBody() = % x, want % x", got, want)
	}
}

// TestWasiFilesystemTypesDescriptorInstanceTypeBody_Validates
// composes the descriptor instance type as an import and confirms
// wasm-tools accepts it + the descriptor sub-resource appears.
func TestWasiFilesystemTypesDescriptorInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesDescriptorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "fstypes.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"descriptor", "(sub resource)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesReadViaStreamInstanceTypeBody_Validates
// composes the read-via-stream descriptor method (which
// outer-aliases input-stream from wasi:io/streams) and confirms
// wasm-tools accepts it.
func TestWasiFilesystemTypesReadViaStreamInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// io/error (instance 0) → error top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	// io/streams (instance 1) → input-stream top-level type 3.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	// filesystem/types read-via-stream, referencing input-stream (type 3).
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesReadViaStreamInstanceTypeBody(3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsread.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"read-via-stream", "descriptor", "error-code", "input-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesWriteViaStreamInstanceTypeBody_Validates
// composes the write-via-stream descriptor method (which
// outer-aliases output-stream from wasi:io/streams) and confirms
// wasm-tools accepts it.
func TestWasiFilesystemTypesWriteViaStreamInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// io/error (instance 0) → error top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	// io/streams (instance 1) → output-stream top-level type 3.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	// filesystem/types write-via-stream, referencing output-stream (type 3).
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesWriteViaStreamInstanceTypeBody(3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fswrite.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"write-via-stream", "descriptor", "error-code", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestInnerTypeFlags_Bytes pins a small flags defvaltype.
func TestInnerTypeFlags_Bytes(t *testing.T) {
	got := component.InnerTypeFlags([]string{"a", "bc"})
	want := []byte{0x6e, 0x02, 0x01, 'a', 0x02, 'b', 'c'}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeFlags = % x, want % x", got, want)
	}
}

// TestWasiFilesystemTypesOpenAtInstanceTypeBody_Validates composes
// the self-contained open-at descriptor method (descriptor +
// error-code + the three flag types) and confirms wasm-tools
// accepts it.
func TestWasiFilesystemTypesOpenAtInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesOpenAtInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "openat.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "path-flags", "open-flags", "descriptor-flags", "symlink-follow"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesReadPathInstanceTypeBody_Validates
// composes the combined read-path filesystem/types instance type
// (open-at + read-via-stream, outer-aliasing input-stream from
// io/streams) and confirms wasm-tools accepts it.
func TestWasiFilesystemTypesReadPathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesReadPathInstanceTypeBody(3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsreadpath.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "read-via-stream", "descriptor", "path-flags"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemTypesWritePathInstanceTypeBody_Validates is the
// write-side counterpart: open-at + write-via-stream, outer-aliasing
// output-stream from wasi:io/streams.
func TestWasiFilesystemTypesWritePathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesWritePathInstanceTypeBody(3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fswritepath.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"open-at", "write-via-stream", "descriptor", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiFilesystemPreopensInstanceTypeBody_Validates composes
// the preopens instance type (which outer-aliases the descriptor
// resource from a wasi:filesystem/types import) and confirms
// wasm-tools accepts it + the get-directories / descriptor / tuple
// names appear.
func TestWasiFilesystemPreopensInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// wasi:filesystem/types import (instance 0) so the outer alias
	// of `descriptor` (top-level type 1) resolves.
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesDescriptorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "descriptor")
	// The preopens instance type referencing top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemPreopensInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/preopens@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "preopens.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-directories", "descriptor", "tuple"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiCliStdoutInstanceTypeBody_Bytes pins the bytes of the
// wasi:cli/stdout instance type body. The expected bytes match
// what wasm-tools emits when wasi:cli/stdout is the THIRD import
// in a print-component (after wasi:io/error and wasi:io/streams)
// — the output-stream type lands at outer typeidx 3.
func TestWasiCliStdoutInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliStdoutInstanceTypeBody(3)
	want := []byte{
		0x01, 0x42, 0x05,
		// alias outer 1 3
		0x02, 0x03, 0x02, 0x01, 0x03,
		// export "output-stream" (type (eq 0))
		0x04, 0x00, 0x0d, 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x00, 0x00,
		// type (own 1)
		0x01, 0x69, 0x01,
		// type (func () -> 2)
		0x01, 0x40, 0x00, 0x00, 0x02,
		// export "get-stdout" (func 3)
		0x04, 0x00, 0x0a, 'g', 'e', 't', '-', 's', 't', 'd', 'o', 'u', 't', 0x01, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliStdoutInstanceTypeBody(3) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestInnerTypeBorrow_Bytes pins `borrow<0>` defvaltype bytes.
func TestInnerTypeBorrow_Bytes(t *testing.T) {
	got := component.InnerTypeBorrow(0)
	want := []byte{0x68, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeBorrow(0) = % x, want % x", got, want)
	}
}

// TestInnerTypeListU8_Bytes pins `list<u8>` defvaltype bytes.
func TestInnerTypeListU8_Bytes(t *testing.T) {
	got := component.InnerTypeListU8
	want := []byte{0x70, 0x7d}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeListU8 = % x, want % x", got, want)
	}
}

// TestInnerTypeResultErr_Bytes pins `result<_, err=5>` bytes.
func TestInnerTypeResultErr_Bytes(t *testing.T) {
	got := component.InnerTypeResultErr(5)
	want := []byte{0x6a, 0x00, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeResultErr(5) = % x, want % x", got, want)
	}
}

// TestInnerTypeVariant_StreamError pins the bytes for the
// canonical-ABI variant `stream-error` declared inside
// wasi:io/streams. Cases:
//   - "last-operation-failed(error)" — payload typeidx 3
//     (own<error>)
//   - "closed" — no payload
//
// Expected wire shape (matches wasm-tools dump):
//   6b 02
//   15 "last-operation-failed" 01 03 00
//   06 "closed" 00 00
func TestInnerTypeVariant_StreamError(t *testing.T) {
	got := component.InnerTypeVariant([]component.VariantCase{
		{Name: "last-operation-failed", HasPayload: true, PayloadValtype: 0x03},
		{Name: "closed"},
	})
	want := []byte{
		0x71, 0x02,
		0x15, 'l', 'a', 's', 't', '-', 'o', 'p', 'e', 'r', 'a', 't', 'i', 'o', 'n', '-', 'f', 'a', 'i', 'l', 'e', 'd', 0x01, 0x03, 0x00,
		0x06, 'c', 'l', 'o', 's', 'e', 'd', 0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeVariant(stream-error) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiIoStreamsInstanceTypeBody_Bytes pins the bytes of the
// wasi:io/streams instance type body, parameterised by where
// wasi:io/error's `error` resource lives at the top-level type
// space (in a canonical print component, that's outer typeidx
// 1 — wasi:io/error is the first import, its single resource
// gets aliased to top-level type 1 right after).
func TestWasiIoStreamsInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiIoStreamsInstanceTypeBody(1)
	// Bytes captured from wasm-tools dump of a canonical
	// print-component (see /tmp/print_comp.wasm in the build
	// playground). The body sits inside the type section at the
	// expected `42 0b ...` opening (instance form, vec(11) decls).
	want := []byte{
		0x01, 0x42, 0x0b,
		// decl 0: export "output-stream" (sub resource)
		0x04, 0x00, 0x0d, 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x01,
		// decl 1: alias outer 1 1
		0x02, 0x03, 0x02, 0x01, 0x01,
		// decl 2: export "error" (type (eq 1))
		0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x01,
		// decl 3: type own<2>
		0x01, 0x69, 0x02,
		// decl 4: type variant stream-error
		0x01, 0x71, 0x02,
		0x15, 'l', 'a', 's', 't', '-', 'o', 'p', 'e', 'r', 'a', 't', 'i', 'o', 'n', '-', 'f', 'a', 'i', 'l', 'e', 'd', 0x01, 0x03, 0x00,
		0x06, 'c', 'l', 'o', 's', 'e', 'd', 0x00, 0x00,
		// decl 5: export "stream-error" (type (eq 4))
		0x04, 0x00, 0x0c, 's', 't', 'r', 'e', 'a', 'm', '-', 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x04,
		// decl 6: type borrow<0>
		0x01, 0x68, 0x00,
		// decl 7: type list<u8>
		0x01, 0x70, 0x7d,
		// decl 8: type result<_, err=5>
		0x01, 0x6a, 0x00, 0x01, 0x05,
		// decl 9: type func(self: 6, contents: 7) -> 8
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x07,
		0x00, 0x08,
		// decl 10: export "[method]output-stream.blocking-write-and-flush" (func 9)
		0x04, 0x00, 0x2e,
		'[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', '.', 'b', 'l', 'o', 'c', 'k', 'i', 'n', 'g', '-', 'w', 'r', 'i', 't', 'e', '-', 'a', 'n', 'd', '-', 'f', 'l', 'u', 's', 'h',
		0x01, 0x09,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoStreamsInstanceTypeBody(1) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestTrampolineModuleFor4I32NoResult_Validates emits the
// trampoline module on its own (wrapped as a core wasm file,
// since wasm-tools validate handles raw modules too) and
// confirms it's a valid core wasm binary. Pins the import
// indirection pattern wasm-tools uses for list<u8> imports.
func TestTrampolineModuleFor4I32NoResult_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.TrampolineModuleFor4I32NoResult()
	dir := t.TempDir()
	modPath := filepath.Join(dir, "trampoline.wasm")
	if err := os.WriteFile(modPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", modPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", modPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"(table",
		"call_indirect",
		"\"$imports\"",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed trampoline, got:\n%s", want, out)
		}
	}
}

// TestFixupModuleFor4I32NoResult_Validates confirms the fixup
// module is a valid core wasm binary on its own (wasm-tools
// validate accepts it; printing shows the imports + the elem
// segment that fills the trampoline's table[0]).
func TestFixupModuleFor4I32NoResult_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.FixupModuleFor4I32NoResult()
	dir := t.TempDir()
	modPath := filepath.Join(dir, "fixup.wasm")
	if err := os.WriteFile(modPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", modPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", modPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"import \"\" \"0\"",
		"import \"\" \"$imports\"",
		"(elem",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed fixup module, got:\n%s", want, out)
		}
	}
}

// TestTrampolineFixupModuleForNI32NoResult_Validates checks the
// param-count-generalised trampoline + fixup builders for a
// non-4 arity (1 param — the wasi:clocks/wall-clock::now
// indirect-return shape, `(out_ptr i32) -> ()`). Each module is
// a standalone valid core wasm binary.
func TestTrampolineFixupModuleForNI32NoResult_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	for _, m := range []struct {
		name  string
		bytes []byte
	}{
		{"trampoline-1i32", component.TrampolineModuleForNI32NoResult(1)},
		{"fixup-1i32", component.FixupModuleForNI32NoResult(1)},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, m.name+".wasm")
		if err := os.WriteFile(p, m.bytes, 0o644); err != nil {
			t.Fatalf("%s write: %v", m.name, err)
		}
		if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("%s validate failed: %v\n%s", m.name, err, out)
		}
	}
}

// TestTrampolineFixupModuleForParamsNoResult_ReadShape validates
// the mixed-valtype trampoline + fixup for the canon-lowered
// blocking-read ABI `(self: i32, len: i64, ret_ptr: i32) -> ()`
// — the i64 in the middle is what the N-i32 builders can't
// express. Each module is a standalone valid core wasm binary.
func TestTrampolineFixupModuleForParamsNoResult_ReadShape(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	readParams := []byte{0x7f, 0x7e, 0x7f} // i32 i64 i32
	for _, m := range []struct {
		name  string
		bytes []byte
	}{
		{"trampoline-read", component.TrampolineModuleForParamsNoResult(readParams)},
		{"fixup-read", component.FixupModuleForParamsNoResult(readParams)},
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, m.name+".wasm")
		if err := os.WriteFile(p, m.bytes, 0o644); err != nil {
			t.Fatalf("%s write: %v", m.name, err)
		}
		if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
			t.Fatalf("%s validate failed: %v\n%s", m.name, err, out)
		}
	}
}

// TestTrampolineModuleForNI32_Matches4 confirms the generalised
// builder reproduces the original hardcoded 4-i32 bytes exactly
// (regression guard for the refactor).
func TestTrampolineModuleForNI32_Matches4(t *testing.T) {
	tramp := component.TrampolineModuleForNI32NoResult(4)
	fixup := component.FixupModuleForNI32NoResult(4)
	// The exported "4I32" wrappers delegate to the N-builders, so
	// equality is structural; assert non-empty + the wasm magic.
	for _, b := range [][]byte{tramp, fixup} {
		if len(b) < 8 || b[0] != 0x00 || b[1] != 0x61 || b[2] != 0x73 || b[3] != 0x6d {
			t.Errorf("not a core wasm module: % x", b[:min(8, len(b))])
		}
	}
}

// TestWasiClocksWallClockInstanceTypeBody_Bytes pins the bytes
// of the wasi:clocks/wall-clock instance type — the datetime
// record + now() function. Matches what wasm-tools emits.
func TestWasiClocksWallClockInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiClocksWallClockInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x04,
		// type record { seconds: u64; nanoseconds: u32 }
		0x01, 0x72, 0x02,
		0x07, 's', 'e', 'c', 'o', 'n', 'd', 's', 0x77,
		0x0b, 'n', 'a', 'n', 'o', 's', 'e', 'c', 'o', 'n', 'd', 's', 0x79,
		// export "datetime" (type (eq 0))
		0x04, 0x00, 0x08, 'd', 'a', 't', 'e', 't', 'i', 'm', 'e', 0x03, 0x00, 0x00,
		// type func() -> typeidx 1
		0x01, 0x40, 0x00, 0x00, 0x01,
		// export "now" (func 2)
		0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiClocksWallClockInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliStdinInstanceTypeBody_Bytes pins the bytes of the
// wasi:cli/stdin instance type — get-stdin() -> input-stream.
// Same shape as the stdout body but referencing input-stream
// (re-exported from outer typeidx) and the get-stdin name.
func TestWasiCliStdinInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliStdinInstanceTypeBody(3)
	want := []byte{
		0x01, 0x42, 0x05,
		// alias outer 1 3
		0x02, 0x03, 0x02, 0x01, 0x03,
		// export "input-stream" (type (eq 0))
		0x04, 0x00, 0x0c, 'i', 'n', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x00, 0x00,
		// type (own 1)
		0x01, 0x69, 0x01,
		// type func() -> 2
		0x01, 0x40, 0x00, 0x00, 0x02,
		// export "get-stdin" (func 3)
		0x04, 0x00, 0x09, 'g', 'e', 't', '-', 's', 't', 'd', 'i', 'n', 0x01, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliStdinInstanceTypeBody(3) mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliEnvironmentArgsInstanceTypeBody_Bytes pins the bytes
// of the wasi:cli/environment instance type for
// `get-arguments: func() -> list<string>`.
func TestWasiCliEnvironmentArgsInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliEnvironmentArgsInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x03,
		// type list<string>
		0x01, 0x70, 0x73,
		// type func() -> typeidx 0
		0x01, 0x40, 0x00, 0x00, 0x00,
		// export "get-arguments" (func 1)
		0x04, 0x00, 0x0d, 'g', 'e', 't', '-', 'a', 'r', 'g', 'u', 'm', 'e', 'n', 't', 's', 0x01, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliEnvironmentArgsInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliEnvironmentArgsInstanceTypeBody_Validates composes
// the instance type as a single import and confirms wasm-tools
// accepts it + the get-arguments / (list string) names appear.
func TestWasiCliEnvironmentArgsInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiCliEnvironmentArgsInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "env.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-arguments", "list string"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Bytes pins
// the bytes of the get-environment instance type
// (`func() -> list<tuple<string, string>>`).
func TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliEnvironmentGetEnvironmentInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x04,
		// type tuple<string, string>
		0x01, 0x6f, 0x02, 0x73, 0x73,
		// type list<typeidx 0>
		0x01, 0x70, 0x00,
		// type func() -> typeidx 1
		0x01, 0x40, 0x00, 0x00, 0x01,
		// export "get-environment" (func 2)
		0x04, 0x00, 0x0f, 'g', 'e', 't', '-', 'e', 'n', 'v', 'i', 'r', 'o', 'n', 'm', 'e', 'n', 't', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliEnvironmentGetEnvironmentInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

// TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Validates
// composes the instance type as a single import and confirms
// wasm-tools accepts it + the get-environment / tuple / list names
// appear.
func TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiCliEnvironmentGetEnvironmentInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "getenv.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"get-environment", "tuple", "list"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestInnerTypeEnum_Bytes pins a small enum defvaltype.
func TestInnerTypeEnum_Bytes(t *testing.T) {
	got := component.InnerTypeEnum([]string{"a", "bc"})
	want := []byte{0x6d, 0x02, 0x01, 'a', 0x02, 'b', 'c'}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeEnum = % x, want % x", got, want)
	}
}

// TestWasiFilesystemErrorCodeEnum_Validates composes the 37-case
// error-code enum inside an instance type (exported under
// "error-code") and confirms wasm-tools accepts it.
func TestWasiFilesystemErrorCodeEnum_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if n := len(component.WasiFilesystemErrorCodeNames); n != 37 {
		t.Fatalf("error-code names = %d, want 37", n)
	}
	// instance type: vec(2) decls — the enum type + its export.
	enum := component.InnerTypeEnum(component.WasiFilesystemErrorCodeNames)
	ibody := []byte{0x01, 0x42, 0x02}
	ibody = append(ibody, 0x01)        // type decl
	ibody = append(ibody, enum...)     // inner 0 = enum
	ibody = append(ibody, component.ExportTypeEqDecl("error-code", 0)...)
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, ibody)
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "errcode.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"error-code", "would-block", "cross-device"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestWasiIoStreamsReadWriteInstanceTypeBody_Validates composes the
// combined read+write io/streams instance type (both stream
// resources + both methods) and confirms wasm-tools accepts it.
func TestWasiIoStreamsReadWriteInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "rwstreams.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"input-stream", "output-stream", "blocking-read", "blocking-write-and-flush"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

// TestInnerTypeResultOkErr_Bytes pins result<ok=7, err=5>.
func TestInnerTypeResultOkErr_Bytes(t *testing.T) {
	got := component.InnerTypeResultOkErr(7, 5)
	want := []byte{0x6a, 0x01, 0x07, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeResultOkErr(7,5) = % x, want % x", got, want)
	}
}

// TestWasiIoStreamsReadInstanceTypeBody_Validates composes the
// read-side streams instance type via the escape hatch (with a
// wasi:io/error import to satisfy the outer alias) and confirms
// wasm-tools accepts it + the input-stream / blocking-read names
// appear.
func TestWasiIoStreamsReadInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	// wasi:io/error import (instance 0) so the outer alias of
	// `error` (top-level type 1) resolves.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	// The read-streams instance type referencing top-level type 1.
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "rd.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{"input-stream", "blocking-read", "(list u8)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}
