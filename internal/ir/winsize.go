package ir

import "github.com/jakechampion/lang/internal/checker"

// WinSizeLayout is the byte offset of each `WinSize` field inside the
// struct's heap field area, plus the area's total size.
//
// Same reason as FsStatLayout beside it: every native backend builds this
// record by hand, with stores at literal offsets.
type WinSizeLayout struct {
	Rows, Cols int32
	Bytes      int32
}

// WinSize is that layout for the checker's current declaration. Both fields
// are i64, so it is the same on every target.
var WinSize = winSizeLayout(8)

func winSizeLayout(ptrW int) WinSizeLayout {
	var offs map[string]int32
	var size int32
	for _, sd := range checker.BuiltinStructDecls() {
		if sd.Name == "WinSize" {
			offs, size = structFieldLayout(sd.Fields, ptrW)
		}
	}
	return WinSizeLayout{
		Rows:  offs["rows"],
		Cols:  offs["cols"],
		Bytes: size,
	}
}
