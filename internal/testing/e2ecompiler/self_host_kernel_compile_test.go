package e2ecompiler

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Milestone 6 of docs/BARE-METAL-PLAN.md (#14265): the arm64-linux compiler,
// unmodified, compiles a program on the kernel. Its output is byte-identical to
// the output of the same compiler on Linux, and the kernel then runs it.
//
// The compile needs more than the 126 MiB of the other boot tests. qemu-virt
// puts the initrd 128 MiB above the start of its RAM, so the kernel reserves
// the frames of the initrd before it allocates.
const (
	compileRAM      = "0x40200000,766M"
	compileRAMBytes = 766 << 20
)

var compileBoardSrc = strings.Replace(processBoardSrc, "pub const RAM_SIZE: usize = 132120576;", fmt.Sprintf("pub const RAM_SIZE: usize = %d;", compileRAMBytes), 1)

const compileProgramSrc = `function main(): i32 {
  print("compiled on fernos");
  return 7;
}
`

// The compile writes the output in the layer in memory. The kernel prints its
// size and FNV-1a hash, and then runs it as a second process.
const compileMainSrc = processKernelSrc + `
function fnv(b: u8[]): u64 {
  let h = 0xcbf29ce484222325 as u64;
  for x in b {
    h = (h ^ x as u64) * 0x100000001b3 as u64;
  }
  return h;
}

function main(): i32 {
  let Some(b) = boot(49152 as usize) else {
    print("no boot");
    return 1;
  };
  let free = b.f.free_count();
  let argv = ["/bin/fern", "-target", "arm64-linux", "-o", "/out/small", "/src/small.fern"];
  let Some(started) = load(b, "/bin/fern", argv) else {
    return 1;
  };
  let p = run_to_end("/bin/fern", started);
  let out = p.written("/out/small");
  release(b, "/bin/fern", p, free);
  let Some(image) = out else {
    print("no output");
    board.power_off(0);
    return 1;
  };
  print("output " + image.len().to_string() + " bytes, fnv " + fnv(image).to_string());
  free = b.f.free_count();
  if let Some(small) = load_image(b, "/out/small", image, ["/out/small"]) {
    release(b, "/out/small", run_to_end("/out/small", small), free);
  }
  board.power_off(0);
  return 0;
}
`

func TestSelfHostKernelCompilesOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	runner := e2eharness.Arm64Runner(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	stage := filepath.Join(dir, "embed")
	if out, err := exec.Command(e2eharness.RepoPath("scripts", "fern-embed-stage"), stage).CombinedOutput(); err != nil {
		t.Fatalf("fern-embed-stage: %v\n%s", err, out)
	}
	compiler := e2eharness.CachedCLIBuild(t, e2eharness.CLIBuild{Target: "arm64-linux", Dir: e2eharness.RepoPath("compiler"), Entry: "fern_main.fern", Embed: stage})
	src := filepath.Join(dir, "small.fern")
	if err := os.WriteFile(src, []byte(compileProgramSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	// The output of the same compiler on Linux, under qemu-user, from an empty
	// cache as on the kernel.
	linuxOut := filepath.Join(dir, "small-linux")
	cmd := exec.Command(compiler, "-target", "arm64-linux", "-o", linuxOut, src)
	if runner != "" {
		cmd = exec.Command(runner, compiler, "-target", "arm64-linux", "-o", linuxOut, src)
	}
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(dir, "home"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile on Linux: %v\n%s", err, msg)
	}
	want, err := os.ReadFile(linuxOut)
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(compiler)
	if err != nil {
		t.Fatal(err)
	}
	files := []cpioFile{{"bin", 0o40755, nil}, {"bin/fern", 0o100755, image}, {"src", 0o40755, nil}, {"src/small.fern", 0o100644, []byte(compileProgramSrc)}, {"out", 0o40755, nil}}
	archive := filepath.Join(dir, "initrd.cpio")
	if err := os.WriteFile(archive, newcArchive(files), 0o644); err != nil {
		t.Fatal(err)
	}
	kernelSrc := writeTickProgram(t, compileBoardSrc, compileMainSrc)
	kernel := filepath.Join(dir, "kernel.img")
	if msg, err := cli.compileCmd("arm64-kernel", kernel, kernelSrc, "-ram", compileRAM, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	start := time.Now()
	out, err := bootQemuFor(t, 15*time.Minute, qemu, "-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", "1G",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel, "-initrd", archive,
		"-global", "virtio-mmio.force-legacy=false", "-device", "virtio-rng-device")
	console := string(out)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	t.Logf("the VM ran for %v", time.Since(start).Round(time.Second))
	h := fnv.New64a()
	h.Write(want)
	for _, line := range []string{
		"/bin/fern exit 0\n",
		"/bin/fern gave back its frames true\n",
		fmt.Sprintf("output %d bytes, fnv %d\n", len(want), h.Sum64()),
		"/out/small fd 1: compiled on fernos\n",
		"/out/small exit 7\n",
		"/out/small gave back its frames true\n",
	} {
		if !strings.Contains(console, line) {
			t.Errorf("console:\n%s\nwant %q", console, line)
		}
	}
}
