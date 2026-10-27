package e2e

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const deflateWindowSource = `import "std/deflate";
import "std/hex";
import "std/i32";
function emit(bits: i32, distance: i32): void {
  let data: u8[] = [113u8, 122u8, 120u8, 65u8];
  let i: i32 = 4;
  while (i < distance) {
    data = data.append(122u8);
    i = i + 1;
  }
  data = data.concat([113u8, 122u8, 120u8, 66u8]);
  let encoded = deflate.deflate_flushed(data[:], bits);
  print(bits.to_string() + " " + distance.to_string() + " " + hex.encode(encoded[:]));
}
function main(): i32 {
  let bits: i32 = 8;
  while (bits <= 15) {
    let window: i32 = 1 << bits;
    emit(bits, window - 1);
    emit(bits, window);
    emit(bits, window + 1);
    bits = bits + 1;
  }
  emit(9, 600);
  emit(9, 8192);
  emit(9, 8193);
  return 0;
}`

// One-byte output chunks force the reference to retain the requested history.
// A fixed invalid-window stream proves that this check is not vacuous.
const deflateWindowReference = `import json, sys, zlib
def check(bits, distance, encoded):
    expected = b'qzxA' + b'z' * (distance - 4) + b'qzxB'
    stream = zlib.decompressobj(-bits)
    pending = bytes.fromhex(encoded) + b'\x01\x00\x00\xff\xff'
    output = bytearray()
    error = ''
    try:
        for _ in range(len(expected) + len(pending) * 16 + 100):
            piece = stream.decompress(pending, 1)
            output.extend(piece)
            pending = stream.unconsumed_tail
            if len(output) > len(expected):
                raise ValueError('excess reference output')
            if stream.eof or not pending and not piece:
                break
    except zlib.error as problem:
        error = str(problem)
    return dict(bits=bits, distance=distance, size=len(output), exact=bytes(output)==expected,
                eof=stream.eof, unused=len(stream.unused_data), error=error)
bad = '2aacaa70ac1a05a380ca0098ae9c00000000ffff'
rows = json.load(sys.stdin)
print(json.dumps(dict(version=zlib.ZLIB_RUNTIME_VERSION, control9=check(9,600,bad),
                     control15=check(15,600,bad), results=[check(**row) for row in rows])))
`

type deflateWindowPacket struct {
	Bits     int    `json:"bits"`
	Distance int    `json:"distance"`
	Encoded  string `json:"encoded"`
}

type deflateWindowObservation struct {
	Bits     int    `json:"bits"`
	Distance int    `json:"distance"`
	Size     int    `json:"size"`
	Exact    bool   `json:"exact"`
	EOF      bool   `json:"eof"`
	Unused   int    `json:"unused"`
	Error    string `json:"error"`
}

func checkDeflateWindows(t *testing.T, stdout, stderr string, code int) {
	t.Helper()
	if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
		t.Fatalf("worker exit %d:\n%s", code, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Fatalf("worker census: %d allocations, %d frees, %d live bytes", allocs, frees, live)
	}
	t.Logf("worker census: %d balanced allocations, zero live bytes", allocs)
	wanted := [][2]int{}
	for bits := 8; bits <= 15; bits++ {
		for delta := -1; delta <= 1; delta++ {
			wanted = append(wanted, [2]int{bits, 1<<bits + delta})
		}
	}
	wanted = append(wanted, [2]int{9, 600}, [2]int{9, 8192}, [2]int{9, 8193})
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if len(lines) != len(wanted) {
		t.Fatalf("worker rows %d, want %d", len(lines), len(wanted))
	}
	packets := make([]deflateWindowPacket, len(wanted))
	for i, pair := range wanted {
		fields := strings.Split(lines[i], " ")
		if len(fields) != 3 || fields[0] != strconv.Itoa(pair[0]) || fields[1] != strconv.Itoa(pair[1]) {
			t.Fatalf("worker row %d identity: %q", i, lines[i])
		}
		encoded, err := hex.DecodeString(fields[2])
		if err != nil || len(encoded) == 0 || hex.EncodeToString(encoded) != fields[2] {
			t.Fatalf("worker row %d encoding: %q", i, lines[i])
		}
		packets[i] = deflateWindowPacket{pair[0], pair[1], fields[2]}
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python with its standard zlib module is required for this reference")
	}
	input, err := json.Marshal(packets)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-c", deflateWindowReference)
	cmd.Stdin = strings.NewReader(string(input))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("streaming reference: %v\n%s", err, output)
	}
	var reference struct {
		Version   string                     `json:"version"`
		Control9  deflateWindowObservation   `json:"control9"`
		Control15 deflateWindowObservation   `json:"control15"`
		Results   []deflateWindowObservation `json:"results"`
	}
	if err := json.Unmarshal(output, &reference); err != nil || len(reference.Results) != len(packets) || reference.Version == "" {
		t.Fatalf("reference protocol: %v\n%s", err, output)
	}
	t.Logf("streaming reference zlib %s", reference.Version)
	t.Run("reference-window-control", func(t *testing.T) {
		if !strings.Contains(reference.Control9.Error, "invalid distance too far back") || reference.Control9.Size != 600 {
			t.Fatalf("9-bit reference accepted the fixed invalid stream: %+v", reference.Control9)
		}
		if !reference.Control15.Exact || !reference.Control15.EOF || reference.Control15.Unused != 0 || reference.Control15.Error != "" {
			t.Fatalf("15-bit reference did not recover the fixed control: %+v", reference.Control15)
		}
	})
	for i, result := range reference.Results {
		packet := packets[i]
		t.Run(fmt.Sprintf("window%d/distance%d", packet.Bits, packet.Distance), func(t *testing.T) {
			if result.Bits != packet.Bits || result.Distance != packet.Distance || result.Error != "" || !result.Exact || !result.EOF || result.Unused != 0 || result.Size != packet.Distance+4 {
				t.Fatalf("streaming result %+v, encoded %s", result, packet.Encoded)
			}
		})
	}
}

func TestDeflateSmallWindowX86_64Sanitized(t *testing.T) {
	out, diagnostic, code := runSanitizeX86_64(t, deflateWindowSource)
	checkDeflateWindows(t, out, diagnostic, code)
}

func TestDeflateSmallWindowArm64Sanitized(t *testing.T) {
	out, diagnostic, code := runSanitizeArm64(t, deflateWindowSource)
	checkDeflateWindows(t, out, diagnostic, code)
}

func TestDeflateSmallWindowWasm(t *testing.T) {
	out, diagnostic, code := runWasmArtifact(t, buildLeakCheckCLIComponent(t, deflateWindowSource, true), runOpts{})
	checkDeflateWindows(t, out, diagnostic, code)
}
