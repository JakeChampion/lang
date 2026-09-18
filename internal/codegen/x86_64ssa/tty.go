package x86_64ssa

import (
	"strconv"

	nativex86_64 "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/ir"
)

// The terminal helpers: the window-size pair and the termios pair, each
// reachable as a free function over a descriptor and as a Reader method.
//
// A descriptor that is not a terminal answers ENOTTY, which is the refusal a
// caller falls back to COLUMNS on. None of these has a path to report, so each
// classifies against an empty string, as every descriptor-shaped failure does.

const (
	ttyIoctl   = 16
	tiocgwinsz = 0x5413
	tiocswinsz = 0x5414
)

// emitHandleTtyHelper returns the emitter for a Reader method that is its free
// function over the descriptor the handle holds: it loads the fd and tail-jumps.
func emitHandleTtyHelper(name, target string) func(w func(string, ...any)) {
	return func(w func(string, ...any)) {
		w("")
		w("%s:", fnLabel(name))
		w("\tmov edi, dword ptr [rdi + 8]") // fd @ ptr+8
		w("\tjmp %s", fnLabel(target))
	}
}

// emitWindowSizeHelper writes window_size(fd) -> Result[WinSize, IoError]: one
// TIOCGWINSZ, whose ws_row and ws_col are adjacent u16s that widen into the
// record. The pixel pair the kernel fills beside them has no field to land in.
//
// rbx = rows, r12 = cols, r13 = the WinSize box.
func emitWindowSizeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("window_size"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tsub rsp, 32")  // the 8-byte winsize, and 16-alignment past three pushes
	w("\tmov edi, edi") // the fd is an i32; ioctl reads the whole register
	w("\tmov esi, %d", tiocgwinsz)
	w("\tmov rdx, rsp")
	w("\tmov eax, %d", ttyIoctl)
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_wsz_err")
	w("\tmovzx ebx, word ptr [rsp]")      // ws_row
	w("\tmovzx r12d, word ptr [rsp + 2]") // ws_col
	ssaBumpAlloc(w, "rax", strconv.Itoa(8+int(ir.WinSize.Bytes)))
	w("\tmov dword ptr [rax], 1") // rc = 1
	w("\tlea r13, [rax + 8]")     // WinSize data
	w("\tmov [r13 + %d], rbx", ir.WinSize.Rows)
	w("\tmov [r13 + %d], r12", ir.WinSize.Cols)
	ssaOptionBox(w, 0, "r13")
	w("\tjmp .Lssa_wsz_ret")
	w(".Lssa_wsz_err:")
	w("\tneg rax")
	ssaFdIoErr(w, 1)
	w(".Lssa_wsz_ret:")
	w("\tadd rsp, 32")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitSetWindowSizeHelper writes set_window_size(fd, rows, cols) ->
// Result[(), IoError]: a TIOCGWINSZ, the two cell counts replaced, and a
// TIOCSWINSZ back.
//
// The read is what keeps the pixel pair the kernel stores beside them: nothing
// surrenders it to a caller, so nothing but this helper can put it back.
// ws_row and ws_col are adjacent u16s, so the pair travels in one register and
// lands in one store. Both counts reach the kernel as u16, so 65536 rows lands
// as 0 rather than a refusal.
//
// A failing first ioctl falls through to the shared check with its errno still
// in rax, which skips the second call. rbx = the fd, r12 = the packed pair.
func emitSetWindowSizeHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("set_window_size"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 24") // the 8-byte winsize, and 16-alignment past two pushes
	w("\tmov ebx, edi")
	w("\tmovzx r12d, si") // rows
	w("\tmovzx ecx, dx")  // cols
	w("\tshl ecx, 16")
	w("\tor r12d, ecx")
	w("\tmov esi, %d", tiocgwinsz)
	w("\tmov rdx, rsp")
	w("\tmov eax, %d", ttyIoctl)
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_swsz_check")
	w("\tmov [rsp], r12d") // ws_row, ws_col; the pixel pair stays put
	w("\tmov edi, ebx")
	w("\tmov esi, %d", tiocswinsz)
	w("\tmov rdx, rsp")
	w("\tmov eax, %d", ttyIoctl)
	w("\tsyscall")
	w(".Lssa_swsz_check:")
	w("\ttest rax, rax")
	w("\tjs .Lssa_swsz_err")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_swsz_ret")
	w(".Lssa_swsz_err:")
	w("\tneg rax")
	ssaFdIoErr(w, 1)
	w(".Lssa_swsz_ret:")
	w("\tadd rsp, 24")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitTermiosGetHelper writes termios_get(fd) -> Result[i64[], IoError]: one
// TCGETS into the kernel's struct, widened element by element into a fresh
// i64[].
//
// The words are the KERNEL's rather than normalised — `stty -g` prints them in
// hex and its restore form reads them back — so the layout is the flat
// emitter's exactly, and both take the struct's shape from its TermiosWords
// and TermiosNCCS.
//
// rbx = the struct, r12 = the array's data.
func emitTermiosGetHelper(w func(string, ...any)) {
	words := nativex86_64.TermiosWords
	w("")
	w("%s:", fnLabel("termios_get"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tsub rsp, 56")  // the 36-byte struct, and 16-alignment past two pushes
	w("\tmov edi, edi") // the fd is an i32; ioctl reads the whole register
	w("\tmov esi, %d", nativex86_64.LinuxTCGETS)
	w("\tmov rdx, rsp")
	w("\tmov eax, %d", ttyIoctl)
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcg_err")
	w("\tmov rbx, rsp")
	// The i64[]: a 16-byte header (cap, rc, len) then one 8-byte word each.
	ssaBumpAlloc(w, "rax", strconv.Itoa(words*8+16))
	w("\tlea r12, [rax + 16]")
	w("\tmov dword ptr [r12 - 12], %d", words) // cap
	w("\tmov dword ptr [r12 - 8], 1")          // rc = 1, a fresh owned array
	w("\tmov dword ptr [r12 - 4], %d", words)  // len
	for i := 0; i < 4; i++ {
		w("\tmov ecx, [rbx + %d]", i*4) // a flag word, zero-extended
		w("\tmov [r12 + %d], rcx", i*8)
	}
	w("\tmovzx ecx, byte ptr [rbx + 16]") // c_line
	w("\tmov [r12 + 32], rcx")
	w("\txor edx, edx")
	w(".Lssa_tcg_cc:")
	w("\tmovzx ecx, byte ptr [rbx + rdx + 17]")
	w("\tmov [r12 + rdx*8 + 40], rcx")
	w("\tinc rdx")
	w("\tcmp rdx, %d", nativex86_64.TermiosNCCS)
	w("\tjb .Lssa_tcg_cc")
	ssaOptionBox(w, 0, "r12")
	w("\tjmp .Lssa_tcg_ret")
	w(".Lssa_tcg_err:")
	w("\tneg rax")
	ssaFdIoErr(w, 1)
	w(".Lssa_tcg_ret:")
	w("\tadd rsp, 56")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}

// emitTermiosSetHelper writes termios_set(fd, when, words) -> Result[(),
// IoError]: the struct packed back out of the word array and handed to TCSETS,
// TCSETSW or TCSETSF as `when` selects.
//
// A wrong-length array or an out-of-range action is EINVAL, as it is on the
// flat emitter: there is a fixed-size struct to fill, and the three ioctls are
// consecutive from TCSETS, so a fourth value would name something else.
//
// rbx = the words, r12 = the action, r13 = the fd.
func emitTermiosSetHelper(w func(string, ...any)) {
	w("")
	w("%s:", fnLabel("termios_set"))
	w("\tpush rbx")
	w("\tpush r12")
	w("\tpush r13")
	w("\tsub rsp, 48") // the 36-byte struct, and 16-alignment past three pushes
	w("\tmov r13d, edi")
	w("\tmov r12d, esi")
	w("\tmov rbx, rdx")
	w("\tmov ecx, %s", memRef("rbx", -4))
	w("\tcmp ecx, %d", nativex86_64.TermiosWords)
	w("\tjne .Lssa_tcs_einval")
	w("\tcmp r12d, 2")
	w("\tja .Lssa_tcs_einval")
	for i := 0; i < 4; i++ {
		w("\tmov rcx, [rbx + %d]", i*8)
		w("\tmov [rsp + %d], ecx", i*4)
	}
	w("\tmov rcx, [rbx + 32]")
	w("\tmov [rsp + 16], cl") // c_line
	w("\txor edx, edx")
	w(".Lssa_tcs_cc:")
	w("\tmov rcx, [rbx + rdx*8 + 40]")
	w("\tmov [rsp + rdx + 17], cl")
	w("\tinc rdx")
	w("\tcmp rdx, %d", nativex86_64.TermiosNCCS)
	w("\tjb .Lssa_tcs_cc")
	w("\tmov edi, r13d")
	w("\tlea esi, [r12 + %d]", nativex86_64.LinuxTCSETS)
	w("\tmov rdx, rsp")
	w("\tmov eax, %d", ttyIoctl)
	w("\tsyscall")
	w("\ttest rax, rax")
	w("\tjs .Lssa_tcs_err")
	ssaOptionBox(w, 0, "")
	w("\tjmp .Lssa_tcs_ret")
	w(".Lssa_tcs_einval:")
	w("\tmov eax, 22") // EINVAL
	w("\tjmp .Lssa_tcs_mkerr")
	w(".Lssa_tcs_err:")
	w("\tneg rax")
	w(".Lssa_tcs_mkerr:")
	ssaFdIoErr(w, 1)
	w(".Lssa_tcs_ret:")
	w("\tadd rsp, 48")
	w("\tpop r13")
	w("\tpop r12")
	w("\tpop rbx")
	w("\tret")
}
