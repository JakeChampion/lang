package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rcCertifyInPlaceScaleSrc = `function build(n: i32): f64[] {
  let xs: f64[] = [];
  let i: i32 = 0;
  while (i < n) {
    xs = xs.append(i as f64);
    i = i + 1;
  }
  return xs;
}

function main(): i32 {
  let v: f64[] = build(17);
  let g: f64[] = __scale_f64(v, 0.75);
  return g.len();
}
`

func TestSelfHostRcCertifyInPlaceScale(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(rcCertifyInPlaceScaleSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := compileCapturing(t, cli, src, "FERN_RC_CERTIFY=report", "FERN_IR_VERIFY=1", "FERN_RC_CERTIFY_DUMP=main")
	if code != 0 {
		t.Fatalf("report-mode compile exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "flat_op scale_f64_in_place ") {
		t.Errorf("the lifted main does not name the in-place scale apart from scale_f64:\n%s", out)
	}
	if !strings.Contains(out, "flat_op scale_f64 ") {
		t.Errorf("the lifted main has no allocating scale on the shared arm:\n%s", out)
	}
	if strings.Contains(out, "FERN_RC_CERTIFY: finding") {
		t.Errorf("the certifier found a fault in a balanced program:\n%s", out)
	}
	if out, code := compileCapturing(t, cli, src); code != 0 {
		t.Errorf("the default compile refused the program (exit %d):\n%s", code, out)
	}
}
