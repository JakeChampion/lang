package ir

import "github.com/jakechampion/lang/internal/checker"

// FsStatLayout is the byte offset of each `FsStat` field inside the
// struct's heap field area, plus the area's total size.
//
// Same reason as FileStatLayout above it: every native backend builds this
// record by hand, with stores at literal offsets, so a field added to the
// checker's declaration would otherwise move `blocks` under three emitters
// still storing it at 8.
type FsStatLayout struct {
	BlockSize, Blocks, BlocksFree, BlocksAvail int32
	Files, FilesFree                           int32
	NameMax, PathMax                           int32
	Bytes                                      int32
}

// FsStat is that layout for the checker's current declaration. Every field is
// an i64, so it is the same on every target.
var FsStat = fsStatLayout(8)

func fsStatLayout(ptrW int) FsStatLayout {
	var offs map[string]int32
	var size int32
	for _, sd := range checker.BuiltinStructDecls() {
		if sd.Name == "FsStat" {
			offs, size = structFieldLayout(sd.Fields, ptrW)
		}
	}
	return FsStatLayout{
		BlockSize:   offs["block_size"],
		Blocks:      offs["blocks"],
		BlocksFree:  offs["blocks_free"],
		BlocksAvail: offs["blocks_avail"],
		Files:       offs["files"],
		FilesFree:   offs["files_free"],
		NameMax:     offs["name_max"],
		PathMax:     offs["path_max"],
		Bytes:       size,
	}
}
