package e2ecompiler

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
// loads the initrd above the RAM that the header of the Image claims, which is
// the -ram range. So the VM has RAM above that range for the initrd.
const compileRAMBytes = 766 << 20

// boardWithRAM is the board of the process tests with ramBytes of RAM.
func boardWithRAM(ramBytes int64) string {
	return strings.Replace(processBoardSrc, "pub const RAM_SIZE: usize = 132120576;", fmt.Sprintf("pub const RAM_SIZE: usize = %d;", ramBytes), 1)
}

const compileProgramSrc = `function main(): i32 {
  print("compiled on fernos");
  return 7;
}
`

// compileKernelSrc runs the compiler with argv on a heap of heapFrames frames.
// The compile writes the output at out, in the layer in memory. The kernel
// prints its size and FNV-1a hash, and with run, runs it as a second process.
func compileKernelSrc(argv []string, out string, heapFrames int, run bool) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = strconv.Quote(a)
	}
	return processKernelSrc + `
function fnv(b: u8[]): u64 {
  let h = 0xcbf29ce484222325 as u64;
  for x in b {
    h = (h ^ x as u64) * 0x100000001b3 as u64;
  }
  return h;
}

function main(): i32 {
  let Some(b) = boot(` + strconv.Itoa(heapFrames) + ` as usize) else {
    print("no boot");
    return 1;
  };
  let free = b.f.free_count();
  let Some(started) = load(b, "/bin/fern", [` + strings.Join(quoted, ", ") + `]) else {
    return 1;
  };
  let p = run_to_end("/bin/fern", started);
  let out = p.written(` + strconv.Quote(out) + `);
  release(b, "/bin/fern", p, free);
  let Some(image) = out else {
    print("no output");
    board.power_off(0);
    return 1;
  };
  print("output " + image.len().to_string() + " bytes, fnv " + fnv(image).to_string());
  if (` + strconv.FormatBool(run) + `) {
    free = b.f.free_count();
    if let Some(small) = load_image(b, ` + strconv.Quote(out) + `, image, [` + strconv.Quote(out) + `]) {
      release(b, ` + strconv.Quote(out) + `, run_to_end(` + strconv.Quote(out) + `, small), free);
    }
  }
  board.power_off(0);
  return 0;
}
`
}

// shippedArm64Compiler builds the arm64-linux compiler with its stdlib embedded,
// and gives its path and the embed stage.
func shippedArm64Compiler(t *testing.T, dir string) (string, string) {
	t.Helper()
	stage := filepath.Join(dir, "embed")
	if out, err := exec.Command(e2eharness.RepoPath("scripts", "fern-embed-stage"), stage).CombinedOutput(); err != nil {
		t.Fatalf("fern-embed-stage: %v\n%s", err, out)
	}
	return e2eharness.CachedCLIBuild(t, e2eharness.CLIBuild{Target: "arm64-linux", Dir: e2eharness.RepoPath("compiler"), Entry: "fern_main.fern", Embed: stage}), stage
}

// linuxCompile runs the arm64-linux compiler with args in dir under qemu-user,
// from an empty cache, and gives the bytes of out.
func linuxCompile(t *testing.T, runner, compiler, dir, out string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(compiler, args...)
	if runner != "" {
		cmd = exec.Command(runner, append([]string{compiler}, args...)...)
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(t.TempDir(), "home"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile on Linux: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(filepath.Join(dir, out))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// bootCompile builds the kernel and boots it with the initrd files, and checks
// that the console has each line of want and the size and hash of output.
func bootCompile(t *testing.T, cli *selfHostCLI, files []cpioFile, kernelSrc string, ramBytes int64, memory string, limit time.Duration, output []byte, want []string) {
	t.Helper()
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	dir := t.TempDir()
	archive := filepath.Join(dir, "initrd.cpio")
	if err := os.WriteFile(archive, newcArchive(files), 0o644); err != nil {
		t.Fatal(err)
	}
	src := writeTickProgram(t, boardWithRAM(ramBytes), kernelSrc)
	kernel := filepath.Join(dir, "kernel.img")
	ram := fmt.Sprintf("0x40200000,%dM", ramBytes>>20)
	if msg, err := cli.compileCmd("arm64-kernel", kernel, src, "-ram", ram, "-emit", "image").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	start := time.Now()
	out, err := bootQemuFor(t, limit, qemu, "-M", "virt,gic-version=3", "-cpu", "cortex-a76", "-m", memory,
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", kernel, "-initrd", archive,
		"-global", "virtio-mmio.force-legacy=false", "-device", "virtio-rng-device")
	console := string(out)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	t.Logf("the VM ran for %v", time.Since(start).Round(time.Second))
	h := fnv.New64a()
	h.Write(output)
	want = append([]string{"/bin/fern exit 0\n", "/bin/fern gave back its frames true\n", fmt.Sprintf("output %d bytes, fnv %d\n", len(output), h.Sum64())}, want...)
	for _, line := range want {
		if !strings.Contains(console, line) {
			t.Errorf("console:\n%s\nwant %q", console, line)
		}
	}
}

func TestSelfHostKernelCompilesOnQemu(t *testing.T) {
	e2eharness.QemuSystem(t, "qemu-system-aarch64")
	runner := e2eharness.Arm64Runner(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	compiler, _ := shippedArm64Compiler(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "small.fern"), []byte(compileProgramSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	want := linuxCompile(t, runner, compiler, dir, "small", "-target", "arm64-linux", "-o", "small", "src/small.fern")
	image, err := os.ReadFile(compiler)
	if err != nil {
		t.Fatal(err)
	}
	files := []cpioFile{{"bin", 0o40755, nil}, {"bin/fern", 0o100755, image}, {"src", 0o40755, nil}, {"src/small.fern", 0o100644, []byte(compileProgramSrc)}, {"out", 0o40755, nil}}
	argv := []string{"/bin/fern", "-target", "arm64-linux", "-o", "/out/small", "/src/small.fern"}
	bootCompile(t, cli, files, compileKernelSrc(argv, "/out/small", 49152, true), compileRAMBytes, "1G", 15*time.Minute, want, []string{
		"/out/small fd 1: compiled on fernos\n",
		"/out/small exit 7\n",
		"/out/small gave back its frames true\n",
	})
}

// treeFiles gives the cpio entries of the tree at root, under prefix: each
// directory, and each regular file with its bytes.
func treeFiles(t *testing.T, root, prefix string) []cpioFile {
	t.Helper()
	var files []cpioFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(prefix, rel))
		if d.IsDir() {
			files = append(files, cpioFile{name, 0o40755, nil})
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, cpioFile{name, 0o100644, b})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The second "done when" item of milestone 6: the compiler compiles itself on
// the kernel, and the result is byte-identical to the result on Linux. A
// self-compile touches about 3 GiB, so the VM has 6 GiB, and the kernel the
// first 5630 MiB of it. The initrd goes in the rest.
const selfCompileRAMBytes = 5630 << 20

func TestSelfHostKernelSelfCompilesOnQemu(t *testing.T) {
	e2eharness.QemuSystem(t, "qemu-system-aarch64")
	runner := e2eharness.Arm64Runner(t)
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	compiler, stage := shippedArm64Compiler(t, dir)
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"compiler": e2eharness.RepoPath("compiler"), "embed": stage} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-target", "arm64-linux", "-embed", "embed", "-o", "fern-self", "compiler/fern_main.fern"}
	want := linuxCompile(t, runner, compiler, root, "fern-self", args...)
	image, err := os.ReadFile(compiler)
	if err != nil {
		t.Fatal(err)
	}
	files := []cpioFile{{"bin", 0o40755, nil}, {"bin/fern", 0o100755, image}, {"out", 0o40755, nil}}
	files = append(files, treeFiles(t, e2eharness.RepoPath("compiler"), "compiler")...)
	files = append(files, treeFiles(t, stage, "embed")...)
	argv := []string{"/bin/fern", "-target", "arm64-linux", "-embed", "embed", "-o", "/out/fern-self", "compiler/fern_main.fern"}
	bootCompile(t, cli, files, compileKernelSrc(argv, "/out/fern-self", 65536, false), selfCompileRAMBytes, "6G", 90*time.Minute, want, nil)
}
