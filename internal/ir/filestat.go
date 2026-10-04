package ir

import "github.com/jakechampion/lang/internal/checker"

// FileStatLayout is the byte offset of each `FileStat` field inside the
// struct's heap field area, plus the area's total size.
//
// Every backend builds this struct by hand — the two arm64 emitters and
// x86-64 with stores at literal offsets, wasmbin with i32/i64.store —
// rather than through the normal struct-literal lowering, so each needs
// the numbers. Deriving them here, in the layer all four import, is what
// stops a field added to the checker's declaration from silently moving
// `size` under four emitters still storing it at 8.
type FileStatLayout struct {
	IsFile, IsDir, Size             int32
	Mode, Nlink, UID, GID           int32
	Dev, Rdev, Ino, Blksize, Blocks int32
	Atime, AtimeNsec                int32
	Mtime, MtimeNsec                int32
	Ctime, CtimeNsec                int32
	Btime, BtimeNsec                int32
	Bytes                           int32
}

// FileStat is that layout for the checker's current declaration. It is the
// same on every target: `boolean` and `u32` take a 4-byte slot, `i64` an
// 8-byte slot aligned to 8, and nothing in the struct is pointer-shaped,
// so ptrW does not enter into it. `TestFileStatLayoutIsTargetIndependent`
// is what fails if that stops being true.
var FileStat = fileStatLayout(8)

func fileStatLayout(ptrW int) FileStatLayout {
	var offs map[string]int32
	var size int32
	for _, sd := range checker.BuiltinStructDecls() {
		if sd.Name == "FileStat" {
			offs, size = structFieldLayout(sd.Fields, ptrW)
		}
	}
	return FileStatLayout{
		IsFile:    offs["is_file"],
		IsDir:     offs["is_dir"],
		Size:      offs["size"],
		Mode:      offs["mode"],
		Nlink:     offs["nlink"],
		UID:       offs["uid"],
		GID:       offs["gid"],
		Dev:       offs["dev"],
		Rdev:      offs["rdev"],
		Ino:       offs["ino"],
		Blksize:   offs["blksize"],
		Blocks:    offs["blocks"],
		Atime:     offs["atime"],
		AtimeNsec: offs["atime_nsec"],
		Mtime:     offs["mtime"],
		MtimeNsec: offs["mtime_nsec"],
		Ctime:     offs["ctime"],
		CtimeNsec: offs["ctime_nsec"],
		Btime:     offs["btime"],
		BtimeNsec: offs["btime_nsec"],
		Bytes:     size,
	}
}

// Linux's `struct statx` is the record every Linux stat helper reads (#9096):
// one layout on every arch, and the one that carries a birth time, which
// `struct stat` has no field for.
const (
	StatxBytes    = 256
	StatxMask     = 0xfff // STATX_BASIC_STATS | STATX_BTIME
	StatxBtimeBit = 0x800 // STATX_BTIME, in stx_mask at offset 0
	StatxModeOff  = 28    // u16
	StatxSizeOff  = 40
	// StatxBtimeOff is stx_btime: seconds at +0, u32 nanoseconds at +8.
	StatxBtimeOff = 80
)

// StatxField is one FileStat field read out of `struct statx`: the FileStat
// offset, the record offset, and how many bytes to load (zero-extended) and
// to store.
type StatxField struct {
	Box, Src, Load, Store int32
}

// StatxFields maps every FileStat field but the kind, the size, the two
// device numbers and the birth time onto `struct statx`.
var StatxFields = []StatxField{
	{FileStat.Mode, StatxModeOff, 2, 4},
	{FileStat.Nlink, 16, 4, 4},
	{FileStat.UID, 20, 4, 4},
	{FileStat.GID, 24, 4, 4},
	{FileStat.Ino, 32, 8, 8},
	{FileStat.Blksize, 4, 4, 8},
	{FileStat.Blocks, 48, 8, 8},
	{FileStat.Atime, 64, 8, 8},
	{FileStat.AtimeNsec, 72, 4, 8},
	{FileStat.Ctime, 96, 8, 8},
	{FileStat.CtimeNsec, 104, 4, 8},
	{FileStat.Mtime, 112, 8, 8},
	{FileStat.MtimeNsec, 120, 4, 8},
}

// StatxDevs are statx's two device numbers, each a (major, minor) pair of
// u32 words that a backend recombines into a dev_t the way glibc's makedev
// does: minor's low byte, major's low 12 bits at 8, minor's remaining bits
// at 20 and major's at 44.
var StatxDevs = []struct{ Box, Major, Minor int32 }{
	{FileStat.Dev, 136, 140},
	{FileStat.Rdev, 128, 132},
}
