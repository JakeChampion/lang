package wasmbin

import (
	"bytes"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
)

// payloadlessArmBoxSize gives, for each helper in helperResultBoxCallers
// that has a PAYLOADLESS arm — an Option's None, a Result's unit Ok — the
// uniform size of the box that arm allocates. Every word of that box past
// the tag has to be written, because the IR's branchless enum drop reads a
// uniform enum's payload words without testing the tag (#8843).
//
// A helper absent from both this map and payloadlessArmAbsent fails below:
// a new result-box helper has to say which it is, the same way
// TestRcResultsCoverEveryRuntimeHelper makes it name its result class.
var payloadlessArmBoxSize = map[string]int32{
	// Option[string]: tag@0, pad@4, data@8, len@12.
	"__fern_env":                 16,
	"__fern_read_line":           16,
	"__fern_reader_read_line_fd": 16,
	// Option[IoError]: tag@0, IoError ptr@4.
	"__fern_writer_write":    8,
	"__fern_writer_close":    8,
	"__fern_reader_close_fd": 8,
}

// payloadlessArmAbsent names the result-box helpers whose every arm carries
// a payload — a Result[T, IoError] where both Ok and Err hold a pointer —
// so there is no uninitialised word for the branchless drop to read.
var payloadlessArmAbsent = map[string]bool{
	"__fern_read_file":         true,
	"__fern_read_file_bytes":   true,
	"__fern_write_file":        true,
	"__fern_open_reader":       true,
	"__fern_open_writer":       true,
	"__fern_open_appender":     true,
	"__fern_open_exclusive":    true,
	"__fern_reader_read_chunk": true,
	"__fern_fd_stat":           true,
	"__fern_reader_seek":       true,
	"__fern_remove_file":       true,
	"__fern_stat":              true,
	"__fern_lstat":             true,
	"__fern_read_dir":          true,
	"__fern_remove_dir_all":    true,
	"__fern_temp_dir":          true,
	"__fern_create_dir_all":    true,
}

// zeroStoreAt is the byte sequence for `i32.const 0; i32.store offset=off`.
// The operand is the literal zero, so a payload store of a real value —
// always a local.get — cannot match it, which is what makes this a test of
// the ZEROING rather than of any store.
func zeroStoreAt(off uint32) []byte {
	b := inst.InstI32Const(nil, 0)
	return memory.InstI32Store(b, 2, off)
}

// A payloadless arm that writes only the tag leaves the rest of the box
// holding whatever the recycled block last contained, and the branchless
// enum drop releases those words as if they were live pointers — the
// out-of-bounds trap of #8843. Every such arm must zero them.
//
// This is the wasm counterpart of the natives' emitted-assembly gates.
// It exists because the first round of #8843 fixed eight of the nine arms:
// __fern_writer_write's None was open-coded like the rest and was missed by
// a hand scan, with nothing failing.
func TestPayloadlessArmsZeroTheirBoxPayload(t *testing.T) {
	// Cross-helper calls resolve through this map; an absent name reads
	// back as funcidx 0, which is fine — nothing here decodes calls.
	idxs := map[string]uint32{}

	names := append([]string(nil), helperResultBoxCallers...)
	sort.Strings(names)

	for _, name := range names {
		size, wants := payloadlessArmBoxSize[name]
		if !wants {
			if !payloadlessArmAbsent[name] {
				t.Errorf("%s is a result-box helper with no payloadless-arm decision: "+
					"add its box size to payloadlessArmBoxSize, or name it in "+
					"payloadlessArmAbsent because every arm of its enum carries a payload",
					name)
			}
			continue
		}
		bodies := map[string]func(map[string]uint32) []byte{}
		if spec, ok := runtimeHelperSpecs[name]; ok && spec.body != nil {
			bodies["preview1"] = spec.body
		}
		if b, ok := preview2HelperBodyOverrides[name]; ok {
			bodies["preview2"] = b
		}
		if len(bodies) == 0 {
			t.Errorf("%s has a payloadless arm but no reachable body to check", name)
			continue
		}
		for world, build := range bodies {
			t.Run(world+"/"+name, func(t *testing.T) {
				code := build(idxs)
				for off := uint32(4); off < uint32(size); off += 4 {
					if !bytes.Contains(code, zeroStoreAt(off)) {
						t.Errorf("%s (%s): the payloadless arm never zeroes the box word at "+
							"+%d. The branchless enum drop reads it without testing the tag, "+
							"so it releases whatever the recycled block held (#8843). Build the "+
							"arm with emitPayloadlessResultBox", name, world, off)
					}
				}
			})
		}
	}
}
