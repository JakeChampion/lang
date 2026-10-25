package e2ecompiler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostJSONContainerDecoders(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.fern": `import "std/json";
import "std/array";
import "std/option";
import "std/result";
@derive(json.Json, json.FromJson)
struct Item { count: i32, label: Option[string] }
function main(): i32 {
 let Ok(items) = json.decode_array[Item]("[{\"count\":2,\"label\":null},{\"count\":3,\"label\":\"fern\"}]") else { return 1; };
 if (items.len()!=2 || items[0].label.is_some() || items[1].label.unwrap_or("")!="fern") { return 2; }
 let Ok(none) = json.decode[Option[i32]]("null") else { return 3; };
 let Ok(nested) = json.decode[Option[Option[i32]]]("7") else { return 4; };
 if (none.is_some() || nested.unwrap_or(None).unwrap_or(0)!=7) { return 5; }
 let Ok(values) = json.decode_array[Option[i32]]("[null,4]") else { return 6; };
 if(values[0].is_some() || values[1].unwrap_or(0)!=4) { return 7; }
 if(json.decode_array[i32]("[1,\"wrong\"]").is_ok() || json.decode_array[i32]("{}").is_ok() || json.decode_array[i32]("[").is_ok()) { return 8; }
 let source=Item { count: 4, label: None };
 if(json.decode[Item](source.to_json()).is_err()) { return 9; }
 return 0;
}`})
	testFractalTargets(t, filepath.Join(dir, "main.fern"))
}

func TestSelfHostVoidLambdaStatementMatch(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.fern": `enum Color { Red, Blue }
function main(): i32 {
 let total=cell_new(0);
 let run=(value: boolean): void => {
   match(value) {
    true => { if(value) { total.set(total.get()+1); } },
    false => { total.set(total.get()+2); }
   }
 };
 run(true);run(false);
 let value=(n:i32):i32 => { match(n) { 0=>{3}, _=>{4} } };
 let c:Option[Color]=Some(Color.Red);
 let matched=match(c){ Some(Color.Red)=>1, Some(other)=>2, None=>3 };
 if(total.get()!=3 || value(0)!=3 || matched!=1){return 1;}
 return 0;
}`})
	testFractalTargets(t, filepath.Join(dir, "main.fern"))
}

func TestSelfHostCellArrayMethods(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"main.fern": `import "std/array";
struct Entry { value: i32 }
function main(): i32 {
 let state=cell_new([Entry {value:2},Entry {value:7}]);
 let entries=state.get();
 let tail=entries.drop(1);
 if(tail.len()!=1 || tail[0].value!=7 || state.get().len()!=2){return 1;}
 return 0;
}`})
	testFractalTargets(t, filepath.Join(dir, "main.fern"))
}

func TestSelfHostBlockyardCore(t *testing.T) {
	path, err := filepath.Abs("../../../examples/showcase/tests/blockyard-core.fern")
	if err != nil {
		t.Fatal(err)
	}
	testFractalTargets(t, path, true)
}

func TestSelfHostBlockyardStore(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source, err := filepath.Abs("../../../examples/showcase/tests/blockyard-store.fern")
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

func TestSelfHostBlockyardWasm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	cli := buildSelfHostCLI(t)
	entry, err := filepath.Abs("../../../examples/showcase/voxel.fern.md")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "voxel.wasm")
	cmd := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-emit", "core-module", "-O", "-o", output, entry, cli.stdlib)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Wasm build: %v\n%s", err, out)
	}
	if out, err := exec.Command(node, "../../../examples/showcase/tests/blockyard-wasm.test.mjs", output).CombinedOutput(); err != nil {
		t.Fatalf("browser kernel: %v\n%s", err, out)
	}
}

func TestSelfHostBlockyardHTTP(t *testing.T) {
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
	data := t.TempDir()
	start := func() (*exec.Cmd, string) {
		cmd := runX86_64Bin(cli.runner, bin, root)
		cmd.Env = append(cmd.Environ(), "FERN_BLOCKYARD_DATA="+data)
		addr, _ := e2eharness.StartInheritedServer(t, cmd)
		return cmd, "http://" + addr
	}
	cmd, base := start()
	response, err := http.Get(base + "/blockyard/status")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	descriptors := func() int {
		entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := descriptors()
	if out, err := exec.Command(node, "../../../examples/showcase/tests/blockyard.load.mjs", base, "512", "3").CombinedOutput(); err != nil {
		t.Fatalf("real participants: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	until := time.Now().Add(5 * time.Second)
	for descriptors() > before && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := descriptors(); after > before {
		t.Fatalf("participant descriptors leaked: %d -> %d", before, after)
	}
	out, err := exec.Command(node, "../../../examples/showcase/tests/blockyard-server.test.mjs", base).CombinedOutput()
	if err != nil {
		t.Fatalf("shared edits: %v\n%s", err, out)
	}
	var edited struct {
		Path         string
		At, Material int
	}
	if err := json.Unmarshal(out, &edited); err != nil {
		t.Fatalf("edit result: %s: %v", out, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, base = start()
	response, err = http.Get(base + edited.Path)
	if err != nil {
		t.Fatal(err)
	}
	var recovered struct {
		Revision int
		Blocks   []struct{ At, Material int }
	}
	err = json.NewDecoder(response.Body).Decode(&recovered)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range recovered.Blocks {
		if block.At == edited.At && block.Material == edited.Material {
			found = true
		}
	}
	if !found || recovered.Revision < 1 {
		t.Fatalf("committed block did not survive process death: %+v", recovered)
	}
}

func TestSelfHostBlockyardWorldDirectory(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("live server gate runs on x86-64 Linux")
	}
	cli := buildSelfHostCLI(t)
	root, err := filepath.Abs("../../../examples")
	if err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, filepath.Join(root, "showcase/server.fern.md"))
	var westTarget, eastTarget *url.URL
	west := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httputil.NewSingleHostReverseProxy(westTarget).ServeHTTP(w, r)
	}))
	defer west.Close()
	east := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httputil.NewSingleHostReverseProxy(eastTarget).ServeHTTP(w, r)
	}))
	defer east.Close()
	manifest := fmt.Sprintf(`[{"name":"commons","server":"west","origin":%q},{"name":"meadow","server":"east","origin":%q}]`, west.URL, east.URL)
	start := func(name string) *url.URL {
		cmd := runX86_64Bin(cli.runner, bin, root)
		cmd.Env = append(cmd.Environ(), "FERN_BLOCKYARD_DATA="+t.TempDir(), "FERN_BLOCKYARD_SERVER="+name, "FERN_BLOCKYARD_WORLDS="+manifest)
		addr, _ := e2eharness.StartInheritedServer(t, cmd)
		target, err := url.Parse("http://" + addr)
		if err != nil {
			t.Fatal(err)
		}
		return target
	}
	westTarget = start("west")
	eastTarget = start("east")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for _, check := range []struct{ from, to, world string }{{west.URL, east.URL, "meadow"}, {east.URL, west.URL, "commons"}} {
		response, err := client.Get(check.from + "/blockyard/?world=" + check.world)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 302 || response.Header.Get("Location") != check.to+"/blockyard/?world="+check.world {
			t.Fatalf("world redirect: %d %s", response.StatusCode, response.Header.Get("Location"))
		}
		response, err = client.Get(check.from + "/blockyard/status?world=" + check.world)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 421 {
			t.Fatalf("wrong owner accepted world: %d", response.StatusCode)
		}
		response, err = client.Get(check.to + "/blockyard/status?world=" + check.world)
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			World      string
			Persistent bool
		}
		err = json.NewDecoder(response.Body).Decode(&status)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !status.Persistent {
			t.Fatalf("owner status: %d %+v", response.StatusCode, status)
		}
	}
}
