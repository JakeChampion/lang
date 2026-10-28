package e2e

import (
	"bytes"
	"debug/elf"
	"testing"
)

// A shared object must keep its code read-execute and its writable
// segments non-executable, declare a non-executable stack, and mark the
// dynamic tables the loader relocates as read-only after relocation.
func TestSharedObjectLayout(t *testing.T) {
	const src = "function answer(): i32 { return 42; }\nfunction main(): i32 { return answer(); }\n"
	for _, tc := range []struct {
		target  string
		machine elf.Machine
	}{{"x86-64-linux", elf.EM_X86_64}, {"arm64-linux", elf.EM_AARCH64}} {
		t.Run(tc.target, func(t *testing.T) {
			so := selfHostShared(t, tc.target, src, "answer")
			f, err := elf.NewFile(bytes.NewReader(so))
			if err != nil {
				t.Fatal(err)
			}
			if f.Type != elf.ET_DYN || f.Machine != tc.machine {
				t.Fatalf("type %v machine %v", f.Type, f.Machine)
			}
			var loads []*elf.Prog
			var dynamic, relro, stack *elf.Prog
			for _, p := range f.Progs {
				switch p.Type {
				case elf.PT_LOAD:
					loads = append(loads, p)
					if p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
						t.Errorf("load segment at %#x is writable and executable", p.Vaddr)
					}
				case elf.PT_DYNAMIC:
					dynamic = p
				case elf.PT_GNU_RELRO:
					relro = p
				case elf.PT_GNU_STACK:
					stack = p
				}
			}
			if len(loads) < 2 || loads[0].Flags != elf.PF_R|elf.PF_X {
				t.Fatalf("want a read-execute code segment first, got %d load segments with flags %v", len(loads), loads[0].Flags)
			}
			if stack == nil || stack.Flags&elf.PF_X != 0 {
				t.Error("no PT_GNU_STACK without PF_X")
			}
			if dynamic == nil {
				t.Fatal("no PT_DYNAMIC")
			}
			if relro == nil {
				t.Fatal("no PT_GNU_RELRO")
			}
			if relro.Vaddr%4096 != 0 || relro.Memsz%4096 != 0 {
				t.Errorf("PT_GNU_RELRO [%#x,+%#x) is not page-aligned, so the loader protects nothing", relro.Vaddr, relro.Memsz)
			}
			if dynamic.Vaddr < relro.Vaddr || dynamic.Vaddr+dynamic.Memsz > relro.Vaddr+relro.Memsz {
				t.Errorf("PT_DYNAMIC [%#x,+%#x) lies outside PT_GNU_RELRO [%#x,+%#x)", dynamic.Vaddr, dynamic.Memsz, relro.Vaddr, relro.Memsz)
			}
			inside := false
			for _, p := range loads {
				if relro.Vaddr >= p.Vaddr && relro.Vaddr+relro.Memsz <= p.Vaddr+p.Memsz {
					inside = true
					if p.Flags&elf.PF_X != 0 {
						t.Error("PT_GNU_RELRO lies in an executable segment")
					}
				}
			}
			if !inside {
				t.Errorf("PT_GNU_RELRO [%#x,+%#x) lies in no load segment", relro.Vaddr, relro.Memsz)
			}
		})
	}
}
