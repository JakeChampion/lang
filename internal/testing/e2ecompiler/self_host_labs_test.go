package e2ecompiler

import (
	"bytes"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostLabsCore(t *testing.T) {
	entry, err := filepath.Abs("../../../examples/showcase/tests/labs-core.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, entry)
}

func TestSelfHostLabsStore(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source, err := filepath.Abs("../../../examples/showcase/tests/labs-store.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, source, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func labWasmKernels(t *testing.T, cli *selfHostCLI) string {
	t.Helper()
	directory := t.TempDir()
	for _, name := range []string{"foundry", "circuit", "patchbay", "prism"} {
		source, err := filepath.Abs("../../../examples/showcase/" + name + ".fern.md")
		if err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(directory, name+".wasm")
		cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "core-module", "-O", "-o", output, source, cli.stdlib)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s Wasm kernel: %v\n%s", name, err, out)
		}
	}
	return directory
}

func TestSelfHostLabsWasm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	directory := labWasmKernels(t, buildSelfHostCLI(t))
	if out, err := exec.Command(node, "../../../examples/showcase/tests/labs-kernels.test.mjs", directory).CombinedOutput(); err != nil {
		t.Fatalf("browser kernels: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}

func TestSelfHostLabsHTTP(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("live server gate runs on x86-64 Linux")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	root, err := filepath.Abs("../../../examples")
	if err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, filepath.Join(root, "showcase/server.fern.md"))
	prepared := filepath.Join(t.TempDir(), "web", ".source")
	if out, err := runX86_64Bin(cli.runner, bin, "--prepare", root, prepared).CombinedOutput(); err != nil {
		t.Fatalf("prepare lessons and thumbnail: %v\n%s", err, out)
	}
	thumbnail, err := os.ReadFile(filepath.Join(prepared, "..", "prism", "preview.png"))
	if err != nil {
		t.Fatal(err)
	}
	image, err := png.DecodeConfig(bytes.NewReader(thumbnail))
	if err != nil || image.Width != 320 || image.Height != 200 {
		t.Fatalf("native thumbnail: %#v, %v", image, err)
	}
	for _, name := range []string{"foundry", "patchbay", "prism", "fernbase", "circuit", "lantern"} {
		lesson, err := os.ReadFile(filepath.Join(prepared, "showcase", name+".fern.md.html"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(lesson, []byte("href=")) || !bytes.Contains(lesson, []byte("Implementation")) {
			t.Fatalf("%s lesson lacks linked code sections", name)
		}
	}
	data := t.TempDir()
	start := func() (*exec.Cmd, string) {
		cmd := runX86_64Bin(cli.runner, bin, root)
		cmd.Env = append(cmd.Environ(), "FERN_LABS_DATA="+data)
		address, _ := e2eharness.StartInheritedServer(t, cmd)
		return cmd, "http://" + address
	}
	cmd, base := start()
	descriptors := func() int {
		entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	if out, err := exec.Command(node, "../../../examples/showcase/tests/labs-server.test.mjs", base).CombinedOutput(); err != nil {
		t.Fatalf("live lab gate: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	if out, err := exec.Command(node, "../../../examples/showcase/tests/labs-rpc.test.mjs", base).CombinedOutput(); err != nil {
		t.Fatalf("persistent command sessions: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	// The reactor creates its epoll and signal descriptors on first use.
	// Establish its baseline after real sessions, then require socket cleanup.
	sockets := func() int {
		entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range entries {
			target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", cmd.Process.Pid, entry.Name()))
			if err == nil && strings.HasPrefix(target, "socket:") {
				count++
			}
		}
		return count
	}
	deadline := time.Now().Add(5 * time.Second)
	for sockets() > 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if count := sockets(); count != 1 {
		t.Fatalf("closed studio sessions retained %d socket descriptors", count)
	}
	before := descriptors()
	if out, err := exec.Command(node, "../../../examples/showcase/tests/labs.load.mjs", base, "256", "3").CombinedOutput(); err != nil {
		t.Fatalf("crowded factory: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	until := time.Now().Add(5 * time.Second)
	for descriptors() > before && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := descriptors(); after > before {
		t.Fatalf("studio descriptors leaked: %d -> %d", before, after)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd, base = start()
	if out, err := exec.Command(node, "../../../examples/showcase/tests/labs-server.test.mjs", base, "recover").CombinedOutput(); err != nil {
		t.Fatalf("process-death recovery: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	nextPath := filepath.Join(data, "fernbase-tests.json.next")
	if err := os.Mkdir(nextPath, 0700); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string, want int) {
		t.Helper()
		response, err := http.Post(base+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != want {
			t.Fatalf("%s returned %d, want %d: %s", path, response.StatusCode, want, data)
		}
	}
	const write = `{"sql":"UPDATE entries SET value='must not publish' WHERE key='alpha';","revision":1,"preview":false}`
	post("/fernbase/query?room=tests", write, 503)
	if err := os.Remove(nextPath); err != nil {
		t.Fatal(err)
	}
	post("/fernbase/query?room=tests", write, 503)
	post("/fernbase/recover?room=tests", `{}`, 200)
	post("/fernbase/query?room=tests", `{"sql":"SELECT * FROM entries WHERE key='alpha';","revision":1,"preview":false}`, 200)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "fernbase-tests.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(bytes, []byte("corrupted")...), 0600); err != nil {
		t.Fatal(err)
	}
	_, base = start()
	response, err := http.Get(base + "/fernbase/state?room=tests")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 503 {
		t.Fatalf("corrupt checkpoint returned %d", response.StatusCode)
	}
}
