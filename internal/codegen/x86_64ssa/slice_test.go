package x86_64ssa

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/ssa"
)

// Cross execution is correctness evidence only. The regular package runner
// intentionally requires native amd64; these new coverage tests also exercise
// the pure-Go assembler and runtime on ARM development hosts with QEMU.
func runSliceModule(t *testing.T, f *ssa.Func, regs int) int {
	t.Helper()
	var runner string
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		for _, name := range []string{"qemu-x86_64", "qemu-x86_64-static"} {
			if path, err := exec.LookPath(name); err == nil {
				runner = path
				break
			}
		}
		if runner == "" {
			t.Skip("requires native Linux amd64 or qemu-x86_64")
		}
	}
	asm, err := EmitAsmModule(map[string]*ssa.Func{f.Name: f}, f.Name, regs, nil)
	if err != nil {
		t.Fatal(err)
	}
	text, data, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("assemble: %v\n%s", err, asm)
	}
	bin := filepath.Join(t.TempDir(), "slice")
	if err := os.WriteFile(bin, nativeelf.StaticExecutableDataX86(text, data), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin)
	if runner != "" {
		cmd = exec.Command(runner, bin)
	}
	out, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() < 0 || len(out) != 0 {
		t.Fatalf("run: %v, state=%v, output=%q", err, cmd.ProcessState, out)
	}
	return cmd.ProcessState.ExitCode()
}

func TestSliceHelpersDiscoverHeapTransitively(t *testing.T) {
	f := ssa.NewFunc("main")
	b := f.NewBlock()
	h := callPtrOp(f, b, "__method_string_as_bytes", constStr(f, b, "x"))
	f.SetRet(b, loadMem(f, b, h, 8, ssa.OpLoad32U))
	asm, err := EmitAsm(f, 4)
	if err != nil {
		t.Fatal(err)
	}
	// There is no explicit allocation in f: the tail-called slice constructor
	// must bring in the heap reservation and guard by its helper dependency.
	for _, symbol := range []string{fnLabel("__method_string_as_bytes"), fnLabel("__slice_make"), heapPtrSym, heapGuardSym} {
		if !strings.Contains(asm, symbol+":") {
			t.Errorf("missing transitive definition %s", symbol)
		}
	}
	if _, _, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr); err != nil {
		t.Fatal(err)
	}
}

func TestSliceHeaderLayoutAndBorrowedData(t *testing.T) {
	for _, regs := range []int{2, 4, 8} {
		t.Run(fmt.Sprint(regs), func(t *testing.T) {
			f := ssa.NewFunc("main")
			b := f.NewBlock()
			data := callPtrOp(f, b, "__alloc_u8", constOp(f, b, 4))
			storeMem(f, b, data, 0, constOp(f, b, 255), ssa.OpStore8)
			h := callPtrOp(f, b, "__slice_make", data, constOp(f, b, 4))
			checks := []ssa.Value{
				f.AddOp(b, ssa.OpEq, loadMem(f, b, h, 0, ssa.OpLoad), data),
				f.AddOp(b, ssa.OpEq, loadMem(f, b, h, 8, ssa.OpLoad32U), constOp(f, b, 4)),
				f.AddOp(b, ssa.OpEq, loadMem(f, b, h, -8, ssa.OpLoad32U), constOp(f, b, 1)),
				f.AddOp(b, ssa.OpEq, loadMem(f, b, h, -4, ssa.OpLoad32U), constOp(f, b, 16)),
			}
			// Dropping one of two owned references releases only the view
			// reference. The backing buffer's reference count stays at one.
			callPtrOp(f, b, "__fern_rc_inc", h)
			callPtrOp(f, b, "__fern_closure_drop", h)
			checks = append(checks,
				f.AddOp(b, ssa.OpEq, loadMem(f, b, h, -8, ssa.OpLoad32U), constOp(f, b, 1)),
				f.AddOp(b, ssa.OpEq, loadMem(f, b, data, -8, ssa.OpLoad32U), constOp(f, b, 1)))
			other := callPtrOp(f, b, "__slice_make", data, constOp(f, b, 1))
			checks = append(checks, f.AddOp(b, ssa.OpNe, other, h))
			// Another allocation must not overwrite the first view's length.
			checks = append(checks, f.AddOp(b, ssa.OpEq, loadMem(f, b, h, 8, ssa.OpLoad32U), constOp(f, b, 4)))
			sum := constOp(f, b, 0)
			for _, check := range checks {
				sum = f.AddOp(b, ssa.OpAdd, sum, check)
			}
			f.SetRet(b, sum)
			if got := runSliceModule(t, f, regs); got != len(checks) {
				t.Fatalf("passed %d layout/ownership checks, want %d", got, len(checks))
			}
		})
	}
}

func TestSliceIndexNormalizesAndChecks(t *testing.T) {
	for _, tc := range []struct {
		helper string
		stride int64
	}{
		{"__slice_idx_1", 1}, {"__slice_idx", 4}, {"__slice_idx_8", 8},
	} {
		for _, index := range []int64{0, 1, 2, -1, -2147483648, 2147483647, 1<<32 | 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.helper, index), func(t *testing.T) {
				f := ssa.NewFunc("main")
				b := f.NewBlock()
				data := callPtrOp(f, b, "__alloc_u8", constOp(f, b, 16))
				storeMem(f, b, data, 0, constOp(f, b, 7), ssa.OpStore8)
				storeMem(f, b, data, tc.stride, constOp(f, b, 9), ssa.OpStore8)
				h := callPtrOp(f, b, "__slice_make", data, constOp(f, b, 2))
				idx := constOp(f, b, index)
				b.Ops[len(b.Ops)-1].Width = 64 // deliberately retain dirty upper bits
				at := callPtrOp(f, b, tc.helper, h, idx)
				f.SetRet(b, loadMem(f, b, at, 0, ssa.OpLoad8U))
				want := 134
				if int32(index) == 0 {
					want = 7
				} else if int32(index) == 1 {
					want = 9
				}
				if got := runSliceModule(t, f, 4); got != want {
					t.Fatalf("got %d, want %d", got, want)
				}
			})
		}
	}
}

func TestSliceRangeNormalizesAndChecks(t *testing.T) {
	for _, tc := range []struct {
		lo, hi, length int64
		want           int
	}{
		{0, 0, 0, 0}, {0, 4, 4, 4}, {4, 4, 4, 0}, {1, 3, 4, 2},
		{-1, 3, 4, 134}, {0, -1, 4, 134}, {3, 1, 4, 134}, {0, 5, 4, 134},
		{-2147483648, 1, 4, 134}, {0, 2147483647, 4, 134},
		{1<<32 | 1, 2<<32 | 3, 3<<32 | 4, 2},
	} {
		t.Run(fmt.Sprintf("%d/%d/%d", tc.lo, tc.hi, tc.length), func(t *testing.T) {
			f := ssa.NewFunc("main")
			b := f.NewBlock()
			var args []ssa.Value
			for _, arg := range []int64{tc.lo, tc.hi, tc.length} {
				args = append(args, constOp(f, b, arg))
				b.Ops[len(b.Ops)-1].Width = 64
			}
			f.SetRet(b, callOp(f, b, "__slice_range", args...))
			if got := runSliceModule(t, f, 2); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
