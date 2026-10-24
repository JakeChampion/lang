package component_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

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
		[]byte{0x00},
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

func TestPutCanonSectionLowerWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerWithMemory(nil, 1, 0)
	want := []byte{0x08, 0x07, 0x01, 0x01, 0x00, 0x01, 0x01, 0x03, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerWithMemory(1, 0) = % x, want % x", got, want)
	}
}

func TestPutCanonResourceDrop_Bytes(t *testing.T) {
	got := component.PutCanonResourceDrop(nil, 1)
	want := []byte{0x08, 0x03, 0x01, 0x03, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonResourceDrop(1) = % x, want % x", got, want)
	}
}

func TestPutCanonSectionLiftWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftWithMemory(nil, 1, 2, 0)
	want := []byte{0x08, 0x08, 0x01, 0x00, 0x00, 0x01, 0x01, 0x03, 0x00, 0x02}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftWithMemory(1,2,0) = % x, want % x", got, want)
	}
}

func TestPutTypeSectionOneDefined_Bytes(t *testing.T) {
	got := component.PutTypeSectionOneDefined(nil, component.InnerTypeList(component.CValtypeS32))
	want := []byte{0x07, 0x03, 0x01, 0x70, 0x7a}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneDefined(list<s32>) = % x, want % x", got, want)
	}
}

func TestPutTypeSectionOneFuncResultIdx_Bytes(t *testing.T) {
	got := component.PutTypeSectionOneFuncResultIdx(nil, []string{"n"}, []byte{component.CValtypeU32}, 0)
	want := []byte{0x07, 0x08, 0x01, 0x40, 0x01, 0x01, 'n', 0x79, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneFuncResultIdx(n:u32 -> #0) = % x, want % x", got, want)
	}
	got = component.PutTypeSectionOneFuncResultIdx(nil, nil, nil, 65)
	want = []byte{0x07, 0x06, 0x01, 0x40, 0x00, 0x00, 0xc1, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneFuncResultIdx(() -> #65) = % x, want % x", got, want)
	}
}

func TestPutTypeSectionOneFuncGeneral_Bytes(t *testing.T) {
	got := component.PutTypeSectionOneFuncGeneral(nil,
		[]string{"xs"}, [][]byte{{0x00}}, []byte{component.CValtypeS32})
	want := []byte{0x07, 0x09, 0x01, 0x40, 0x01, 0x02, 'x', 's', 0x00, 0x00, 0x7a}
	if !bytes.Equal(got, want) {
		t.Errorf("PutTypeSectionOneFuncGeneral(xs:#0 -> s32) = % x, want % x", got, want)
	}
}

func TestPutCanonSectionLiftWithMemoryRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftWithMemoryRealloc(nil, 1, 2, 0, 3)
	want := []byte{0x08, 0x0a, 0x01, 0x00, 0x00, 0x01, 0x02, 0x03, 0x00, 0x04, 0x03, 0x02}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftWithMemoryRealloc(1,2,0,3) = % x, want % x", got, want)
	}
}

func TestPutCanonResourceDrop_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutCanonResourceDrop(buf, 1)
	dir := t.TempDir()
	p := filepath.Join(dir, "rdrop.wasm")
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
	if !bytes.Contains(out, []byte("resource.drop")) {
		t.Errorf("expected resource.drop in printed component, got:\n%s", out)
	}
}

func TestPutCanonSectionLowerWithMemoryRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerWithMemoryRealloc(nil, 2, 0, 5)
	want := []byte{0x08, 0x09, 0x01, 0x01, 0x00, 0x02, 0x02, 0x03, 0x00, 0x04, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerWithMemoryRealloc(2, 0, 5) = % x, want % x", got, want)
	}
}

func TestPutAliasSectionInstanceExportType_Bytes(t *testing.T) {
	got := component.PutAliasSectionInstanceExportType(nil, 0, "error")
	want := []byte{0x06, 0x0a, 0x01, 0x03, 0x00, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r'}
	if !bytes.Equal(got, want) {
		t.Errorf("PutAliasSectionInstanceExportType(0, %q) = % x, want % x", "error", got, want)
	}
}

func TestOuterAliasTypeDecl_Bytes(t *testing.T) {
	got := component.OuterAliasTypeDecl(1, 1)
	want := []byte{0x02, 0x03, 0x02, 0x01, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("OuterAliasTypeDecl(1, 1) = % x, want % x", got, want)
	}
}

func TestExportSubResourceDecl_Bytes(t *testing.T) {
	got := component.ExportSubResourceDecl("error")
	want := []byte{0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("ExportSubResourceDecl(%q) = % x, want % x", "error", got, want)
	}
}

func TestExportTypeEqDecl_Bytes(t *testing.T) {
	got := component.ExportTypeEqDecl("error", 1)
	want := []byte{0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("ExportTypeEqDecl(%q, 1) = % x, want % x", "error", got, want)
	}
}

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

func TestWasiIoPollInstanceTypeBody_WithPoll_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(true))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "poll.wasm")
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
	for _, want := range []string{"\"poll\"", "(list u32)", "(list 1)"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiIoPollInstanceTypeBody_BlockOnly_Bytes(t *testing.T) {
	got := component.WasiIoPollInstanceTypeBody(false)
	want := []byte{
		0x01, 0x42, 0x04,
		0x04, 0x00, 0x08, 'p', 'o', 'l', 'l', 'a', 'b', 'l', 'e', 0x03, 0x01,
		0x01, 0x68, 0x00,
		0x01, 0x40, 0x01, 0x04, 's', 'e', 'l', 'f', 0x01, 0x01, 0x00,
		0x04, 0x00, 0x16, '[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'p', 'o', 'l', 'l', 'a', 'b', 'l', 'e', '.', 'b', 'l', 'o', 'c', 'k', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoPollInstanceTypeBody(false) = % x, want % x", got, want)
	}
}

func TestPutCanonSectionLowerAsync_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerAsync(nil, 0, 0)
	want := []byte{
		0x08, 0x08,
		0x01,
		0x01, 0x00,
		0x00,
		0x02,
		0x06,
		0x03, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerAsync(0, 0) = % x, want % x", got, want)
	}
}

func TestPutCanonSectionLiftAsync_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftAsync(nil, 1, 0)
	want := []byte{
		0x08, 0x07,
		0x01,
		0x00, 0x00,
		0x01,
		0x01, 0x06,
		0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftAsync(1, 0) = % x, want % x", got, want)
	}
}

func TestPutCanonTaskReturnSingle_Bytes(t *testing.T) {
	got := component.PutCanonTaskReturnSingle(nil, component.CValtypeU32)
	want := []byte{
		0x08, 0x05,
		0x01,
		0x09,
		0x00, component.CValtypeU32,
		0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonTaskReturnSingle(u32) = % x, want % x", got, want)
	}
}

func TestPutCanonSectionLowerAsyncRealloc_Bytes(t *testing.T) {
	got := component.PutCanonSectionLowerAsyncRealloc(nil, 0, 0, 0)
	want := []byte{
		0x08, 0x0a,
		0x01,
		0x01, 0x00,
		0x00,
		0x03,
		0x06,
		0x03, 0x00,
		0x04, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLowerAsyncRealloc(0,0,0) = % x, want % x", got, want)
	}
}

func TestPutCanonTaskReturnStringWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonTaskReturnStringWithMemory(nil, 0)
	want := []byte{
		0x08, 0x07,
		0x01,
		0x09,
		0x00, 0x73,
		0x01,
		0x03, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonTaskReturnStringWithMemory(0) = % x, want % x", got, want)
	}
}

func TestPutCanonSectionLiftAsyncWithMemory_Bytes(t *testing.T) {
	got := component.PutCanonSectionLiftAsyncWithMemory(nil, 1, 0, 0)
	want := []byte{
		0x08, 0x09,
		0x01,
		0x00, 0x00,
		0x01,
		0x02,
		0x06,
		0x03, 0x00,
		0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("PutCanonSectionLiftAsyncWithMemory(1,0,0) = % x, want % x", got, want)
	}
}

func TestPutCanonWaitableBuiltins_Bytes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"waitable-set.new", component.PutCanonWaitableSetNew(nil), []byte{0x08, 0x02, 0x01, 0x1f}},
		{"waitable-set.wait", component.PutCanonWaitableSetWait(nil, 0), []byte{0x08, 0x04, 0x01, 0x20, 0x00, 0x00}},
		{"waitable-set.drop", component.PutCanonWaitableSetDrop(nil), []byte{0x08, 0x02, 0x01, 0x22}},
		{"waitable.join", component.PutCanonWaitableJoin(nil), []byte{0x08, 0x02, 0x01, 0x23}},
		{"subtask.drop", component.PutCanonSubtaskDrop(nil), []byte{0x08, 0x02, 0x01, 0x0d}},
		{"thread.yield", component.PutCanonThreadYield(nil), []byte{0x08, 0x03, 0x01, 0x0c, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = % x, want % x", c.name, c.got, c.want)
		}
	}
}

func TestPutCanonFutureStreamBuiltins_Bytes(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"future.new", component.PutCanonFutureNew(nil, 0), []byte{0x08, 0x03, 0x01, 0x15, 0x00}},
		{"future.read", component.PutCanonFutureRead(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x16, 0x00, 0x02, 0x06, 0x03, 0x00}},
		{"future.write", component.PutCanonFutureWrite(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x17, 0x00, 0x02, 0x06, 0x03, 0x00}},
		{"stream.new", component.PutCanonStreamNew(nil, 0), []byte{0x08, 0x03, 0x01, 0x0e, 0x00}},
		{"stream.read", component.PutCanonStreamRead(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x0f, 0x00, 0x02, 0x06, 0x03, 0x00}},
		{"stream.write", component.PutCanonStreamWrite(nil, 0, 0), []byte{0x08, 0x07, 0x01, 0x10, 0x00, 0x02, 0x06, 0x03, 0x00}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = % x, want % x", c.name, c.got, c.want)
		}
	}
	if got := component.InnerTypeFuture(component.CValtypeU32); !bytes.Equal(got, []byte{0x65, 0x01, 0x79}) {
		t.Errorf("InnerTypeFuture(u32) = % x, want 65 01 79", got)
	}
	if got := component.InnerTypeStream(component.CValtypeU8); !bytes.Equal(got, []byte{0x66, 0x01, 0x7d}) {
		t.Errorf("InnerTypeStream(u8) = % x, want 66 01 7d", got)
	}
	if got := component.PutCanonTaskReturnTypeIdx(nil, 0); !bytes.Equal(got, []byte{0x08, 0x05, 0x01, 0x09, 0x00, 0x00, 0x00}) {
		t.Errorf("PutCanonTaskReturnTypeIdx(0) = % x, want 08 05 01 09 00 00 00", got)
	}
}

func TestWasiClocksMonotonicTimerInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiClocksMonotonicTimerInstanceTypeBody(5)
	want := []byte{
		0x01, 0x42, 0x04,
		0x02, 0x03, 0x02, 0x01, 0x05,
		0x01, 0x69, 0x00,
		0x01, 0x40, 0x01, 0x04, 'w', 'h', 'e', 'n', component.CValtypeU64, 0x00, 0x01,
		0x04, 0x00, 0x12, 's', 'u', 'b', 's', 'c', 'r', 'i', 'b', 'e', '-', 'd', 'u', 'r', 'a', 't', 'i', 'o', 'n', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiClocksMonotonicTimerInstanceTypeBody(5) = % x, want % x", got, want)
	}
}

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

func TestWasiFilesystemTypesReadViaStreamInstanceTypeBody_Validates(t *testing.T) {
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

func TestWasiFilesystemTypesReadWritePathInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(4, 3, component.FsFeatures{OpenAt: true, Read: true, Write: true}))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 5)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsrw.wasm")
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
	for _, want := range []string{"read-via-stream", "write-via-stream", "descriptor", "input-stream", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiFilesystemTypesWriteViaStreamInstanceTypeBody_Validates(t *testing.T) {
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

func TestInnerTypeFlags_Bytes(t *testing.T) {
	got := component.InnerTypeFlags([]string{"a", "bc"})
	want := []byte{0x6e, 0x02, 0x01, 'a', 0x02, 'b', 'c'}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeFlags = % x, want % x", got, want)
	}
}

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
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(3, 0, component.FsFeatures{OpenAt: true, Read: true}))
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
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(0, 3, component.FsFeatures{OpenAt: true, Write: true}))
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

func TestWasiFilesystemTypesAppendPathInstanceTypeBody_Validates(t *testing.T) {
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
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesPathInstanceTypeBody(0, 3, component.FsFeatures{OpenAt: true, Append: true}))
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 4)
	dir := t.TempDir()
	p := filepath.Join(dir, "fsappendpath.wasm")
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
	for _, want := range []string{"open-at", "append-via-stream", "descriptor", "output-stream"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiFilesystemPreopensInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiFilesystemTypesDescriptorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:filesystem/types@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "descriptor")
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

func TestWasiCliStdoutInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliStdoutInstanceTypeBody(3)
	want := []byte{
		0x01, 0x42, 0x05,
		0x02, 0x03, 0x02, 0x01, 0x03,
		0x04, 0x00, 0x0d, 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x00, 0x00,
		0x01, 0x69, 0x01,
		0x01, 0x40, 0x00, 0x00, 0x02,
		0x04, 0x00, 0x0a, 'g', 'e', 't', '-', 's', 't', 'd', 'o', 'u', 't', 0x01, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliStdoutInstanceTypeBody(3) mismatch\ngot  % x\nwant % x", got, want)
	}
}

func TestInnerTypeBorrow_Bytes(t *testing.T) {
	got := component.InnerTypeBorrow(0)
	want := []byte{0x68, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeBorrow(0) = % x, want % x", got, want)
	}
}

func TestInnerTypeListU8_Bytes(t *testing.T) {
	got := component.InnerTypeListU8
	want := []byte{0x70, 0x7d}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeListU8 = % x, want % x", got, want)
	}
}

func TestInnerTypeResultErr_Bytes(t *testing.T) {
	got := component.InnerTypeResultErr(5)
	want := []byte{0x6a, 0x00, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeResultErr(5) = % x, want % x", got, want)
	}
}

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

func TestWasiIoStreamsInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiIoStreamsInstanceTypeBody(1)
	want := []byte{
		0x01, 0x42, 0x0b,
		0x04, 0x00, 0x0d, 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x01,
		0x02, 0x03, 0x02, 0x01, 0x01,
		0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x01,
		0x01, 0x69, 0x02,
		0x01, 0x71, 0x02,
		0x15, 'l', 'a', 's', 't', '-', 'o', 'p', 'e', 'r', 'a', 't', 'i', 'o', 'n', '-', 'f', 'a', 'i', 'l', 'e', 'd', 0x01, 0x03, 0x00,
		0x06, 'c', 'l', 'o', 's', 'e', 'd', 0x00, 0x00,
		0x04, 0x00, 0x0c, 's', 't', 'r', 'e', 'a', 'm', '-', 'e', 'r', 'r', 'o', 'r', 0x03, 0x00, 0x04,
		0x01, 0x68, 0x00,
		0x01, 0x70, 0x7d,
		0x01, 0x6a, 0x00, 0x01, 0x05,
		0x01, 0x40, 0x02,
		0x04, 's', 'e', 'l', 'f', 0x06,
		0x08, 'c', 'o', 'n', 't', 'e', 'n', 't', 's', 0x07,
		0x00, 0x08,
		0x04, 0x00, 0x2e,
		'[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', '.', 'b', 'l', 'o', 'c', 'k', 'i', 'n', 'g', '-', 'w', 'r', 'i', 't', 'e', '-', 'a', 'n', 'd', '-', 'f', 'l', 'u', 's', 'h',
		0x01, 0x09,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiIoStreamsInstanceTypeBody(1) mismatch\ngot  % x\nwant % x", got, want)
	}
}

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

func TestTrampolineFixupModuleForParamsNoResult_ReadShape(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	readParams := []byte{0x7f, 0x7e, 0x7f}
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

func TestTrampolineModuleForNI32_Matches4(t *testing.T) {
	tramp := component.TrampolineModuleForNI32NoResult(4)
	fixup := component.FixupModuleForNI32NoResult(4)
	for _, b := range [][]byte{tramp, fixup} {
		if len(b) < 8 || b[0] != 0x00 || b[1] != 0x61 || b[2] != 0x73 || b[3] != 0x6d {
			t.Errorf("not a core wasm module: % x", b[:min(8, len(b))])
		}
	}
}

func TestWasiClocksWallClockInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiClocksWallClockInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x04,
		0x01, 0x72, 0x02,
		0x07, 's', 'e', 'c', 'o', 'n', 'd', 's', 0x77,
		0x0b, 'n', 'a', 'n', 'o', 's', 'e', 'c', 'o', 'n', 'd', 's', 0x79,
		0x04, 0x00, 0x08, 'd', 'a', 't', 'e', 't', 'i', 'm', 'e', 0x03, 0x00, 0x00,
		0x01, 0x40, 0x00, 0x00, 0x01,
		0x04, 0x00, 0x03, 'n', 'o', 'w', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiClocksWallClockInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

func TestWasiCliStdinInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliStdinInstanceTypeBody(3)
	want := []byte{
		0x01, 0x42, 0x05,
		0x02, 0x03, 0x02, 0x01, 0x03,
		0x04, 0x00, 0x0c, 'i', 'n', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', 0x03, 0x00, 0x00,
		0x01, 0x69, 0x01,
		0x01, 0x40, 0x00, 0x00, 0x02,
		0x04, 0x00, 0x09, 'g', 'e', 't', '-', 's', 't', 'd', 'i', 'n', 0x01, 0x03,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliStdinInstanceTypeBody(3) mismatch\ngot  % x\nwant % x", got, want)
	}
}

func TestWasiCliEnvironmentArgsInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliEnvironmentArgsInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x03,
		0x01, 0x70, 0x73,
		0x01, 0x40, 0x00, 0x00, 0x00,
		0x04, 0x00, 0x0d, 'g', 'e', 't', '-', 'a', 'r', 'g', 'u', 'm', 'e', 'n', 't', 's', 0x01, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliEnvironmentArgsInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

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

func TestWasiCliEnvironmentGetEnvironmentInstanceTypeBody_Bytes(t *testing.T) {
	got := component.WasiCliEnvironmentGetEnvironmentInstanceTypeBody()
	want := []byte{
		0x01, 0x42, 0x04,
		0x01, 0x6f, 0x02, 0x73, 0x73,
		0x01, 0x70, 0x00,
		0x01, 0x40, 0x00, 0x00, 0x01,
		0x04, 0x00, 0x0f, 'g', 'e', 't', '-', 'e', 'n', 'v', 'i', 'r', 'o', 'n', 'm', 'e', 'n', 't', 0x01, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("WasiCliEnvironmentGetEnvironmentInstanceTypeBody() mismatch\ngot  % x\nwant % x", got, want)
	}
}

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

func TestWasiCliEnvironmentArgsAndEnvInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiCliEnvironmentArgsAndEnvInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:cli/environment@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "argsenv.wasm")
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
	for _, want := range []string{"get-arguments", "get-environment", "tuple", "list string"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestInnerTypeEnum_Bytes(t *testing.T) {
	got := component.InnerTypeEnum([]string{"a", "bc"})
	want := []byte{0x6d, 0x02, 0x01, 'a', 0x02, 'b', 'c'}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeEnum = % x, want % x", got, want)
	}
}

func TestWasiFilesystemErrorCodeEnum_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if n := len(component.WasiFilesystemErrorCodeNames); n != 37 {
		t.Fatalf("error-code names = %d, want 37", n)
	}
	enum := component.InnerTypeEnum(component.WasiFilesystemErrorCodeNames)
	ibody := []byte{0x01, 0x42, 0x02}
	ibody = append(ibody, 0x01)
	ibody = append(ibody, enum...)
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

func TestWasiIoPollInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "pollable")
	buf = component.PutCanonResourceDrop(buf, 1)
	dir := t.TempDir()
	p := filepath.Join(dir, "poll.wasm")
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
	for _, want := range []string{"pollable", "pollable.block", "resource.drop"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiHttpValueTypesInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiHttpValueTypesInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:http/types@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "httptypes.wasm")
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
	for _, want := range []string{
		"method", "scheme", "header-error", "error-code",
		"DNS-error-payload", "TLS-alert-received-payload", "field-size-payload",
		"HTTP-response-body-size", "internal-error",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestBuildHttpIncomingHandlerComponent_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	stub := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x06, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x0a, 0x01, 0x06, 'h', 'a', 'n', 'd', 'l', 'e', 0x00, 0x00,
		0x0a, 0x04, 0x01, 0x02, 0x00, 0x0b,
	}
	comp := component.BuildHttpIncomingHandlerComponent(stub, "handle")
	dir := t.TempDir()
	p := filepath.Join(dir, "handler.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", p).CombinedOutput(); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	}
	out, err := exec.Command("wasm-tools", "print", p).CombinedOutput()
	if err != nil {
		t.Fatalf("print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"wasi:http/incoming-handler@0.2.0", "wasi:http/types@0.2.0", "handle",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiHttpTypesInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiHttpTypesInstanceTypeBody(4, 3))
	buf = component.PutImportSectionOneInstance(buf, "wasi:http/types@0.2.0", 5)
	dir := t.TempDir()
	p := filepath.Join(dir, "httptypes.wasm")
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
	for _, want := range []string{
		"incoming-request", "incoming-body", "future-trailers", "outgoing-response",
		"outgoing-body", "response-outparam", "fields",
		"path-with-query", "set-status-code", "response-outparam.set",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiSocketsNetworkInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 0)
	dir := t.TempDir()
	p := filepath.Join(dir, "network.wasm")
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
	for _, want := range []string{"network", "error-code", "ip-address-family", "ip-socket-address"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiSocketsInstanceNetworkInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "network")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsInstanceNetworkInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/instance-network@0.2.0", 2)
	dir := t.TempDir()
	p := filepath.Join(dir, "instnet.wasm")
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
	if !bytes.Contains(out, []byte("instance-network")) {
		t.Errorf("expected instance-network in printed component, got:\n%s", out)
	}
}

func TestWasiSocketsTcpInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "pollable")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 7)
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "network")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "error-code")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-socket-address")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpInstanceTypeBody(8, 9, 10, 4, 3, 6))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp@0.2.0", 11)
	dir := t.TempDir()
	p := filepath.Join(dir, "tcp.wasm")
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
	for _, want := range []string{"tcp-socket", "start-bind", "start-listen", "accept", "subscribe"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiSocketsTcpConnectInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "pollable")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 7)
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "network")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "error-code")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-socket-address")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpConnectInstanceTypeBody(8, 9, 10, 4, 3, 6))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp@0.2.0", 11)
	dir := t.TempDir()
	p := filepath.Join(dir, "tcp_connect.wasm")
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
	for _, want := range []string{"start-connect", "finish-connect", "start-bind", "subscribe"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestWasiSocketsTcpCreateSocketInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoStreamsReadWriteInstanceTypeBody(1))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/streams@0.2.0", 2)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "output-stream")
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "input-stream")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "pollable")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 7)
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "network")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "error-code")
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-socket-address")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpInstanceTypeBody(8, 9, 10, 4, 3, 6))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp@0.2.0", 11)
	buf = component.PutAliasSectionInstanceExportType(buf, 3, "ip-address-family")
	buf = component.PutAliasSectionInstanceExportType(buf, 4, "tcp-socket")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsTcpCreateSocketInstanceTypeBody(12, 9, 13))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/tcp-create-socket@0.2.0", 14)
	dir := t.TempDir()
	p := filepath.Join(dir, "tcpcreate.wasm")
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
	if !bytes.Contains(out, []byte("create-tcp-socket")) {
		t.Errorf("expected create-tcp-socket in printed component, got:\n%s", out)
	}
}

func TestWasiSocketsUdpInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsNetworkInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/network@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "network")
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error-code")
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "ip-socket-address")
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "ip-address-family")
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoPollInstanceTypeBody(false))
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/poll@0.2.0", 5)
	buf = component.PutAliasSectionInstanceExportType(buf, 1, "pollable")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsUdpInstanceTypeBody(1, 2, 3, 6))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/udp@0.2.0", 7)
	buf = component.PutAliasSectionInstanceExportType(buf, 2, "udp-socket")
	buf = component.PutTypeSectionRawBody(buf, component.WasiSocketsUdpCreateSocketInstanceTypeBody(4, 2, 8))
	buf = component.PutImportSectionOneInstance(buf, "wasi:sockets/udp-create-socket@0.2.0", 9)
	dir := t.TempDir()
	p := filepath.Join(dir, "udp.wasm")
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
	for _, want := range []string{
		"udp-socket", "incoming-datagram-stream", "outgoing-datagram-stream",
		"udp-socket.start-bind", "udp-socket.stream",
		"outgoing-datagram-stream.check-send", "outgoing-datagram-stream.send",
		"outgoing-datagram-stream.subscribe",
		"create-udp-socket",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, out)
		}
	}
}

func TestInnerTypeTuple_Bytes(t *testing.T) {
	got := component.InnerTypeTuple([]byte{component.CValtypeU8, component.CValtypeU8, component.CValtypeU8, component.CValtypeU8})
	want := []byte{0x6f, 0x04, component.CValtypeU8, component.CValtypeU8, component.CValtypeU8, component.CValtypeU8}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeTuple = % x, want % x", got, want)
	}
}

func TestInnerTypeRecord_Bytes(t *testing.T) {
	got := component.InnerTypeRecord([]component.RecordField{
		{Name: "port", Valtype: component.CValtypeU16},
		{Name: "address", Valtype: 0x05},
	})
	want := []byte{0x72, 0x02,
		0x04, 'p', 'o', 'r', 't', component.CValtypeU16,
		0x07, 'a', 'd', 'd', 'r', 'e', 's', 's', 0x05,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeRecord = % x, want % x", got, want)
	}
}

func TestInnerTypeResultOkErr_Bytes(t *testing.T) {
	got := component.InnerTypeResultOkErr(7, 5)
	want := []byte{0x6a, 0x01, 0x07, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("InnerTypeResultOkErr(7,5) = % x, want % x", got, want)
	}
}

func TestWasiIoStreamsReadInstanceTypeBody_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, component.WasiIoErrorInstanceTypeBody())
	buf = component.PutImportSectionOneInstance(buf, "wasi:io/error@0.2.0", 0)
	buf = component.PutAliasSectionInstanceExportType(buf, 0, "error")
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

func TestTrampolineFixupModuleForParamsResults_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	params := []byte{0x7f, 0x7f}
	results := []byte{0x7f}
	for _, m := range []struct {
		name  string
		bytes []byte
	}{
		{"trampoline-result", component.TrampolineModuleForParamsResults(params, results)},
		{"fixup-result", component.FixupModuleForParamsResults(params, results)},
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
	if !bytes.Equal(component.TrampolineModuleForParamsResults(params, nil), component.TrampolineModuleForParamsNoResult(params)) {
		t.Errorf("results=nil trampoline must match the NoResult builder byte-for-byte")
	}
	if !bytes.Equal(component.FixupModuleForParamsResults(params, nil), component.FixupModuleForParamsNoResult(params)) {
		t.Errorf("results=nil fixup must match the NoResult builder byte-for-byte")
	}
}
