package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const loadHelloSource = `import "std/http";
import "std/platform";
import "std/serve";

function main(): i32 {
  return serve.exit_code(serve.run(0, serve.config(), (req: HttpRequest, plat: platform.Host): HttpResponse => http.ok("hello")));
}
`

func loadExampleSource(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("../../../examples/load/load.fern")
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func writeLoadHello(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "hello.fern")
	if err := os.WriteFile(src, []byte(loadHelloSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// TestLoadExampleX86_64 runs examples/load with -q against a std/serve hello,
// both built by the self-host compiler.
func TestLoadExampleX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	hello := filepath.Join(dir, "hello.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", hello, writeLoadHello(t)).CombinedOutput(); err != nil {
		t.Fatalf("hello build failed: %v\n%s", err, o)
	}
	load := filepath.Join(dir, "load.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", load, loadExampleSource(t)).CombinedOutput(); err != nil {
		t.Fatalf("load build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, hello))
	e2eharness.WaitServerReady(t, addr)
	checkLoadExample(t, runX86Bin(qemu, load, "-q", "200", "-c", "4", "-z", "2s", "-w", "--output-format", "json", "http://"+addr+"/"), 400)
}

// TestLoadExampleInterp runs examples/load and the hello under -interp. It
// also checks that a started connect (`net.connect_start`) gives a
// non-blocking socket there, as it does natively: with a blocking one the
// tool reads past the first response and sends nothing more.
func TestLoadExampleInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", writeLoadHello(t)))
	e2eharness.WaitServerReady(t, addr)
	checkLoadExample(t, exec.Command(bin, "-interp", loadExampleSource(t), "--", "-q", "50", "-c", "2", "-z", "2s", "-w", "--output-format", "json", "http://"+addr+"/"), 100)
}

// checkLoadExample checks a JSON summary of `scheduled` requests on the
// timetable of -q. The tool can send fewer when it is behind at the end.
func checkLoadExample(t *testing.T, cmd *exec.Cmd, scheduled int) {
	t.Helper()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("load: %v\n%s%s", err, out, &stderr)
	}
	var got struct {
		Summary struct {
			SuccessRate float64 `json:"successRate"`
		} `json:"summary"`
		LatencyPercentiles     map[string]float64 `json:"latencyPercentiles"`
		StatusCodeDistribution map[string]int     `json:"statusCodeDistribution"`
		ErrorDistribution      map[string]int     `json:"errorDistribution"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	done := got.StatusCodeDistribution["200"]
	if len(got.StatusCodeDistribution) != 1 || len(got.ErrorDistribution) != 0 || got.Summary.SuccessRate != 1 {
		t.Errorf("want only 200 responses and no errors\n%s", out)
	}
	if done > scheduled || done < scheduled/2 {
		t.Errorf("%d responses, want %d scheduled and at least half of them sent\n%s", done, scheduled, out)
	}
	p := got.LatencyPercentiles
	if p["p50"] <= 0 || p["p50"] > p["p99"] || p["p99"] > p["p99.99"] {
		t.Errorf("latency not ordered: p50 %v p99 %v p99.99 %v\n%s", p["p50"], p["p99"], p["p99.99"], out)
	}
}
