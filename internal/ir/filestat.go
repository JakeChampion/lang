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
		Bytes:     size,
	}
}
