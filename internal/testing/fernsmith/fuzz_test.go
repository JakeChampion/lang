package fernsmith_test

import (
	"encoding/binary"
	"testing"

	"github.com/jakechampion/lang/internal/testing/fernsmith"
)

func FuzzGenerate_ParseRoundTrips(f *testing.F) {
	for seed := uint64(0); seed < 32; seed++ {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Add(b[:])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		src := fernsmith.GenBytes(data)
		if err := checkGenerated(t, src); err != nil {
			t.Fatalf("generated program failed to parse/type-check:\ndata=%x\nsrc:\n%s\nerr: %v", data, src, err)
		}
	})
}
