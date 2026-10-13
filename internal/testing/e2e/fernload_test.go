// tools/fernload, the open-loop load generator, against a std/serve hello
// server: every request the rate schedules goes out, every one is
// answered, and the report carries the counts and a corrected latency
// distribution. The x86-64 leg builds both programs with the self-host
// compiler; the interpreter leg runs the tool under `-interp`, where a
// started connect is non-blocking from the start as it is natively.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const fernloadHelloSource = `import "std/http";
import "std/platform";
import "std/serve";

function main(): i32 {
  return serve.run(0, serve.config(), (req: HttpRequest, plat: platform.Host): HttpResponse => http.ok("hello"));
}
`

func fernloadSource(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs("../../../tools/fernload/fernload.fern")
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func writeFernloadHello(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "hello.fern")
	if err := os.WriteFile(src, []byte(fernloadHelloSource), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestFernloadX86_64(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := x86QemuOrEmpty(t)
	dir := t.TempDir()
	hello := filepath.Join(dir, "hello.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", hello, writeFernloadHello(t)).CombinedOutput(); err != nil {
		t.Fatalf("hello build failed: %v\n%s", err, o)
	}
	load := filepath.Join(dir, "fernload.bin")
	if o, err := exec.Command(bin, "-target", "x86-64-linux", "-o", load, fernloadSource(t)).CombinedOutput(); err != nil {
		t.Fatalf("fernload build failed: %v\n%s", err, o)
	}
	addr, _ := e2eharness.StartInheritedServer(t, runX86Bin(qemu, hello))
	e2eharness.WaitServerReady(t, addr, 10*time.Second)
	checkFernload(t, runX86Bin(qemu, load, "--rate", "200", "--connections", "4", "--duration", "2", "http://"+addr+"/"), 400)
}

func TestFernloadInterp(t *testing.T) {
	bin := buildLangBinForInterp(t)
	addr, _ := e2eharness.StartInheritedServer(t, exec.Command(bin, "-interp", writeFernloadHello(t)))
	e2eharness.WaitServerReady(t, addr, 10*time.Second)
	checkFernload(t, exec.Command(bin, "-interp", fernloadSource(t), "--", "--rate", "50", "--connections", "2", "--duration", "2", "http://"+addr+"/"), 100)
}

// checkFernload runs the tool and reads its report: every scheduled
// request sent and answered, none lost, and a latency distribution that
// is ordered and non-empty.
func checkFernload(t *testing.T, cmd *exec.Cmd, scheduled int) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fernload: %v\n%s", err, out)
	}
	rows := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, value, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("not a report row: %q\n%s", line, out)
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("row %s: %v\n%s", name, err, out)
		}
		rows[name] = n
	}
	for _, want := range []string{"load/sent", "load/behind", "load/requests", "load/errors", "load/rps", "load/p50_us", "load/p90_us", "load/p99_us", "load/p999_us", "load/max_us", "load/raw_p99_us"} {
		if _, ok := rows[want]; !ok {
			t.Errorf("no %s row\n%s", want, out)
		}
	}
	if rows["load/sent"] != scheduled || rows["load/behind"] != 0 {
		t.Errorf("sent %d behind %d, want %d sent and none behind\n%s", rows["load/sent"], rows["load/behind"], scheduled, out)
	}
	if rows["load/requests"] != rows["load/sent"] || rows["load/errors"] != 0 {
		t.Errorf("requests %d errors %d, want every one of %d answered\n%s", rows["load/requests"], rows["load/errors"], rows["load/sent"], out)
	}
	if rows["load/p50_us"] <= 0 || rows["load/p50_us"] > rows["load/p99_us"] || rows["load/p99_us"] > rows["load/max_us"] {
		t.Errorf("latency not ordered: p50 %d p99 %d max %d\n%s", rows["load/p50_us"], rows["load/p99_us"], rows["load/max_us"], out)
	}
	if rows["load/rps"] < scheduled/2/2 {
		t.Errorf("rps %d is under half the rate\n%s", rows["load/rps"], out)
	}
}
