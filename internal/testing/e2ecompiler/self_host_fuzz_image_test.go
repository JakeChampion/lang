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

// imageCheck reads the summary of an image run, `READER static N pie N g N`,
// and checks each image of size N above 0 that the driver wrote beside the
// input. A size of 0 is a refusal, which must have a cause line.
func imageCheck(want imageLayout) readerCheck {
	return func(r readerRun) string {
		fields := strings.Fields(r.summary)
		if len(fields) != 7 {
			return fmt.Sprintf("the driver printed %q", r.summary)
		}
		var faults []string
		for i := 1; i < len(fields); i += 2 {
			kind := fields[i]
			n, err := strconv.Atoi(fields[i+1])
			if err != nil {
				return fmt.Sprintf("the driver printed %q", r.summary)
			}
			if n == 0 {
				if !strings.Contains(r.stderr, "fern: ") {
					faults = append(faults, fmt.Sprintf("the %s image was refused with no cause\nstderr: %q", kind, r.stderr))
				}
				continue
			}
			if fault := imageFault(r.path+"."+kind, kind, want); fault != "" {
				faults = append(faults, kind+" image: "+fault)
			}
		}
		if len(faults) > 0 {
			return strings.Join(faults, "\n") + fmt.Sprintf("\ninput: %q", r.input)
		}
		return ""
	}
}

// FuzzSelfHostX86Image takes hostile text through the x86-64 assembler, the
// linker and the ELF writer.
func FuzzSelfHostX86Image(f *testing.F) { fuzzReaderTarget(f) }

// FuzzSelfHostArm64Image takes hostile text through the arm64 assembler, the
// linker and the ELF writer.
func FuzzSelfHostArm64Image(f *testing.F) { fuzzReaderTarget(f) }

// TestSelfHostImageOfCompiledProgram runs the image mode on the assembly of a
// compiled program, which shows that the checks accept what a real compile
// produces. Assembly compiled without -pie gives the static and the -g image,
// and its -pie link is refused with a cause, not an exit of the driver.
// Assembly compiled with -pie gives all three images. Text with no _start, or
// with no code at it, gives no image on either ISA.
func TestSelfHostImageOfCompiledProgram(t *testing.T) {
	for _, c := range []struct {
		which, target string
	}{{"x86", "x86-64-linux"}, {"arm64", "arm64-linux"}} {
		t.Run(c.which, func(t *testing.T) {
			r := readerTargetNamed(t, c.which+"-image")
			bin, stdlib := fuzzCLI(t)
			driver := fuzzDriver(t, "asm_fuzz_run")
			run := func(text []byte, kinds ...string) {
				t.Helper()
				path := filepath.Join(t.TempDir(), "text.s")
				if err := os.WriteFile(path, text, 0o644); err != nil {
					t.Fatal(err)
				}
				if fault := readerFault(r, text, path, runUnderBudget(exec.Command(driver, r.reader, path))); fault != "" {
					t.Fatal(fault)
				}
				var written []string
				for _, kind := range []string{"static", "pie", "g"} {
					if _, err := os.Stat(path + "." + kind); err == nil {
						written = append(written, kind)
					}
				}
				if got, want := strings.Join(written, " "), strings.Join(kinds, " "); got != want {
					t.Errorf("the images written are %q, want %q", got, want)
				}
			}
			run(compiledAssembly(t, bin, stdlib, c.target), "static", "g")
			run(compiledAssembly(t, bin, stdlib, c.target, "-pie"), "static", "pie", "g")
			run([]byte(".text\nf:\n ret\n"))
			run([]byte(".text\n_start:\n"))
		})
	}
}
