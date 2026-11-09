package e2ecompiler

import (
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The targets here take hostile assembly text through the in-process
// assembler, the linker and the ELF writer, as a native compile does, with
// drivers/asm_fuzz_run.fern. The driver writes a static, a -pie and a -g
// image. Each image that the writer accepts must parse with Go's debug/elf
// and keep the layout that the loader needs: segments inside the file,
// addresses that agree with offsets, no segment both writable and executable,
// and an entry in the code.

// imageLayout is the layout that each image of one target must keep.
type imageLayout struct {
	machine elf.Machine
	page    uint64
}

var (
	x86Layout   = imageLayout{elf.EM_X86_64, 0x1000}
	arm64Layout = imageLayout{elf.EM_AARCH64, 0x10000}
)

// imageFault returns the first rule that the image at path breaks, or "".
func imageFault(path string, kind string, want imageLayout) string {
	info, err := os.Stat(path)
	if err != nil {
		return err.Error()
	}
	size := uint64(info.Size())
	f, err := elf.Open(path)
	if err != nil {
		return "debug/elf cannot read the image: " + err.Error()
	}
	defer f.Close()
	wantType := elf.ET_EXEC
	if kind == "pie" {
		wantType = elf.ET_DYN
	}
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != want.machine || f.Type != wantType {
		return fmt.Sprintf("the header is %v %v %v %v, want ELFCLASS64 ELFDATA2LSB %v %v", f.Class, f.Data, f.Machine, f.Type, want.machine, wantType)
	}
	var loads []*elf.Prog
	stack := false
	for _, p := range f.Progs {
		if p.Off+p.Filesz > size || p.Off+p.Filesz < p.Off {
			return fmt.Sprintf("a %v segment ends at %#x, past the file of %#x bytes", p.Type, p.Off+p.Filesz, size)
		}
		if p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
			return fmt.Sprintf("a %v segment at %#x is writable and executable", p.Type, p.Vaddr)
		}
		switch p.Type {
		case elf.PT_LOAD:
			if p.Filesz > p.Memsz {
				return fmt.Sprintf("a load segment at %#x has %#x bytes in the file and %#x in memory", p.Vaddr, p.Filesz, p.Memsz)
			}
			if p.Align != want.page || p.Vaddr%want.page != p.Off%want.page {
				return fmt.Sprintf("a load segment at %#x from offset %#x has alignment %#x, want the two to agree modulo %#x", p.Vaddr, p.Off, p.Align, want.page)
			}
			if n := len(loads); n > 0 && p.Vaddr < loads[n-1].Vaddr+loads[n-1].Memsz {
				return fmt.Sprintf("the load segment at %#x overlaps the one at %#x", p.Vaddr, loads[n-1].Vaddr)
			}
			loads = append(loads, p)
		case elf.PT_GNU_STACK:
			stack = true
		}
	}
	if len(loads) == 0 || !stack {
		return fmt.Sprintf("the image has %d load segments and stack segment %v", len(loads), stack)
	}
	inLoad := func(lo, hi uint64, exec bool) bool {
		for _, p := range loads {
			if lo >= p.Vaddr && hi <= p.Vaddr+p.Filesz && (!exec || p.Flags&elf.PF_X != 0) {
				return true
			}
		}
		return false
	}
	if !inLoad(f.Entry, f.Entry+1, true) {
		return fmt.Sprintf("the entry %#x is not in the code", f.Entry)
	}
	for _, p := range f.Progs {
		if (p.Type == elf.PT_GNU_RELRO || p.Type == elf.PT_GNU_EH_FRAME) && p.Memsz > 0 {
			hi := p.Vaddr + p.Filesz
			if p.Type == elf.PT_GNU_RELRO {
				hi = p.Vaddr + p.Memsz
			}
			if !inLoadMem(loads, p.Vaddr, hi) {
				return fmt.Sprintf("the %v segment at %#x is not inside a load segment", p.Type, p.Vaddr)
			}
		}
	}
	for _, s := range f.Sections {
		if s.Type != elf.SHT_NOBITS && s.Type != elf.SHT_NULL && (s.Offset+s.Size > size || s.Offset+s.Size < s.Offset) {
			return fmt.Sprintf("section %s ends at %#x, past the file of %#x bytes", s.Name, s.Offset+s.Size, size)
		}
	}
	return ""
}

func inLoadMem(loads []*elf.Prog, lo, hi uint64) bool {
	for _, p := range loads {
		if lo >= p.Vaddr && hi <= p.Vaddr+p.Memsz {
			return true
		}
	}
	return false
}

// imageFaults runs the image mode of the driver on one input and returns the
// faults of its run and of each image it wrote.
func imageFaults(driver, which string, want imageLayout, work string, input []byte) []string {
	path := filepath.Join(work, "text.s")
	if err := os.WriteFile(path, input, 0o644); err != nil {
		return []string{err.Error()}
	}
	out := filepath.Join(work, "out")
	os.RemoveAll(out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return []string{err.Error()}
	}
	r := runUnderBudget(exec.Command(driver, which+"-image", path, out))
	if fault := budgetFault("asm_fuzz_run "+which+"-image", r, input); fault != "" {
		return []string{fault}
	}
	if r.code == 134 && strings.HasPrefix(r.stderr, "fern: ") {
		return nil
	}
	fields := strings.Fields(r.stdout)
	if r.code != 0 || len(fields) != 7 || fields[0] != which+"-image" {
		return []string{fmt.Sprintf("exit %d on %d bytes, want 0 with the image sizes or 134 with a cause\nstdout: %q\nstderr: %q", r.code, len(input), r.stdout, r.stderr)}
	}
	var faults []string
	for i := 1; i < len(fields); i += 2 {
		kind := fields[i]
		n, err := strconv.Atoi(fields[i+1])
		if err != nil {
			return []string{fmt.Sprintf("the driver printed %q", r.stdout)}
		}
		if n == 0 {
			if !strings.Contains(r.stderr, "fern: ") {
				faults = append(faults, fmt.Sprintf("the %s image was refused with no cause\nstderr: %q", kind, r.stderr))
			}
			continue
		}
		if fault := imageFault(filepath.Join(out, kind), kind, want); fault != "" {
			faults = append(faults, kind+" image: "+fault)
		}
	}
	return faults
}

// fuzzImage shares the body of the two image targets. The seeds are the
// assembler targets' seeds: the assembly of a compiled program and short
// texts that probe labels, sections and alignment.
func fuzzImage(f *testing.F, which, target string, want imageLayout) {
	bin, stdlib := fuzzCLI(f)
	driver := fuzzDriver(f, "asm_fuzz_run")
	for _, seed := range assemblerSeeds(f, bin, stdlib, target) {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > fuzzInputLimit {
			t.Skip("over the input limit")
		}
		for _, fault := range imageFaults(driver, which, want, t.TempDir(), input) {
			t.Errorf("%s\ninput: %q", fault, input)
		}
	})
}

// FuzzSelfHostX86Image takes hostile text through the x86-64 assembler, the
// linker and the ELF writer.
func FuzzSelfHostX86Image(f *testing.F) { fuzzImage(f, "x86", "x86-64-linux", x86Layout) }

// FuzzSelfHostArm64Image takes hostile text through the arm64 assembler, the
// linker and the ELF writer.
func FuzzSelfHostArm64Image(f *testing.F) { fuzzImage(f, "arm64", "arm64-linux", arm64Layout) }

// TestSelfHostImageOfCompiledProgram runs the image mode on the assembly of a
// compiled program, which shows that the checks accept what a real compile
// produces. Assembly compiled without -pie gives the static and the -g image,
// and its -pie link is refused with a cause, not an exit of the driver.
// Assembly compiled with -pie gives the -pie image. Text with no _start gives
// no image on either ISA.
func TestSelfHostImageOfCompiledProgram(t *testing.T) {
	for _, c := range []struct {
		which, target string
		want          imageLayout
	}{{"x86", "x86-64-linux", x86Layout}, {"arm64", "arm64-linux", arm64Layout}} {
		t.Run(c.which, func(t *testing.T) {
			bin, stdlib := fuzzCLI(t)
			driver := fuzzDriver(t, "asm_fuzz_run")
			run := func(text []byte, kinds ...string) string {
				t.Helper()
				work := t.TempDir()
				if faults := imageFaults(driver, c.which, c.want, work, text); len(faults) > 0 {
					t.Fatal(strings.Join(faults, "\n"))
				}
				var written []string
				for _, kind := range []string{"static", "pie", "g"} {
					if _, err := os.Stat(filepath.Join(work, "out", kind)); err == nil {
						written = append(written, kind)
					}
				}
				got := strings.Join(written, " ")
				if want := strings.Join(kinds, " "); got != want {
					t.Errorf("the images written are %q, want %q", got, want)
				}
				return got
			}
			run(compiledAssembly(t, bin, stdlib, c.target), "static", "g")
			run(compiledAssembly(t, bin, stdlib, c.target, "-pie"), "pie")
			run([]byte(".text\nf:\n ret\n"))
		})
	}
}
