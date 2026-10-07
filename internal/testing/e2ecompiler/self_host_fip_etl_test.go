package e2ecompiler

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func etlDataset(n int) []byte {
	data := make([]byte, 8*n)
	for i := 0; i < n; i++ {
		sensor, quality := i%16, i%4
		if i%19 == 0 {
			sensor = 255
		}
		if i%23 == 0 {
			quality = 7
		}
		data[8*i], data[8*i+1] = byte(sensor), byte(quality)
		binary.LittleEndian.PutUint16(data[8*i+2:], uint16(int16(i*173%20000-5000)))
		binary.LittleEndian.PutUint32(data[8*i+4:], uint32(i))
	}
	return data
}

func etlOracle(data []byte) (invalid, filtered, accepted, total, low, high int, histogram [16]int, out []byte) {
	low, high = 16501, -1
	for at := 0; at < len(data); at += 8 {
		v := int(int16(binary.LittleEndian.Uint16(data[at+2:])))
		if data[at] >= 16 || data[at+1] >= 4 || v < -4000 || v > 12500 {
			invalid++
			continue
		}
		if data[at+1] < 2 {
			filtered++
			continue
		}
		v += 4000
		accepted++
		total += v
		if v < low {
			low = v
		}
		if v > high {
			high = v
		}
		bin := v / 1024
		if bin > 15 {
			bin = 15
		}
		histogram[bin]++
		encoded := make([]byte, 8)
		binary.LittleEndian.PutUint32(encoded, uint32(v))
		copy(encoded[4:], data[at+4:at+8])
		out = append(out, encoded...)
	}
	return
}

func etlCoreForTest(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"etl_core.fern", "etl_variants.fern", "etl.fern"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "fip", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSelfHostFipETL(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const count = 257
	data := etlDataset(count)
	invalid, filtered, accepted, total, low, high, hist, encoded := etlOracle(data)
	for _, variant := range []struct {
		name, process string
		zeroAlloc     bool
	}{
		{"fip", "etl_core.process", true},
		{"fbip", "etl_variants.process_fbip", true},
		{"baseline", "etl_variants.process_baseline", false},
		{"batched", "etl_variants.process_batched", false},
	} {
		t.Run(variant.name, func(t *testing.T) {
			for _, batch := range []int{1, 7, 64, 257} {
				t.Run(fmt.Sprintf("batch-%d", batch), func(t *testing.T) {
					var src strings.Builder
					fmt.Fprintf(&src, "import \"./etl_core\";\nimport \"./etl_variants\";\nfunction main(): i32 {\nlet data: u8[] = etl_core.dataset(%d);\n", count)
					fmt.Fprintf(&src, "let expected_data: u8[] = %s;\nlet expected_output: u8[] = %s;\nlet check: i32 = 0;\nwhile (check < data.len()) { if (data[check] != expected_data[check]) { return 10; } check = check + 1; }\n", etlByteLiteral(data), etlByteLiteral(encoded))
					fmt.Fprintf(&src, "let s: etl_core.State = etl_core.new_state(%d);\ns = etl_core.process(s, data, 0, %d);\ns = etl_core.reset(s);\nlet mark: i64 = __heap_alloc_count();\n", batch, batch)
					outputAt := 0
					for first := 0; first < count; first += batch {
						n := min(batch, count-first)
						fmt.Fprintf(&src, "s = etl_core.process(s, data, %d, %d);\n", first*8, n)
						_, _, _, _, _, _, _, out := etlOracle(data[first*8 : (first+n)*8])
						fmt.Fprintf(&src, "if (s.output_count != %d) { return 11; }\n", len(out)/8)
						fmt.Fprintf(&src, "check = 0;\nwhile (check < %d) { if (s.output[check] != expected_output[%d + check]) { return 12; } check = check + 1; }\n", len(out), outputAt)
						outputAt += len(out)
					}
					if variant.zeroAlloc {
						src.WriteString("if (__heap_alloc_count() != mark) { return 13; }\n")
					}
					fmt.Fprintf(&src, "if (s.seen != %di64 || s.invalid != %di64 || s.filtered != %di64 || s.accepted != %di64 || s.total != %di64 || s.low != %d || s.high != %d) { return 14; }\n", count, invalid, filtered, accepted, total, low, high)
					for bin, n := range hist {
						fmt.Fprintf(&src, "if (s.histogram[%d] != %di64) { return 15; }\n", bin, n)
					}
					fmt.Fprintf(&src, "let saved: etl_core.State = s;\ns = etl_core.process(s, data, 0, %d);\nif (saved.seen != %di64 || saved.total != %di64 || saved.histogram[0] != %di64) { return 16; }\n", batch, count, total, hist[0])
					src.WriteString("let seen: i64 = s.seen;\ns = etl_core.process(s, data, 0, 0);\nif (s.seen != seen || s.output_count != 0) { return 17; }\n")
					fmt.Fprintf(&src, "s = etl_core.process(s, data, 0, %d);\n", batch+1)
					src.WriteString("s = etl_core.process(s, data, -1, 1);\ns = etl_core.process(s, data, data.len() - 1, 1);\ns = etl_core.process(s, data, 0, -1);\nif (s.refused != 4i64 || s.seen != seen || s.output_count != 0) { return 18; }\nreturn 0;\n}\n")
					dir := t.TempDir()
					etlCoreForTest(t, dir)
					path := filepath.Join(dir, "etl_test.fern")
					program := strings.ReplaceAll(src.String(), "etl_core.process(", variant.process+"(")
					if err := os.WriteFile(path, []byte(program), 0600); err != nil {
						t.Fatal(err)
					}
					for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
						t.Run(target, func(t *testing.T) {
							stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
							if code != 0 {
								t.Fatalf("exit %d: %s", code, stderr)
							}
							assertBalancedCensus(t, stderr)
						})
					}
				})
			}
		})
	}
}

func etlByteLiteral(data []byte) string {
	var s strings.Builder
	s.WriteString("[")
	for i, b := range data {
		if i != 0 {
			s.WriteString(", ")
		}
		fmt.Fprintf(&s, "%du8", b)
	}
	s.WriteString("]")
	return s.String()
}

func TestSelfHostFipETLBoundaries(t *testing.T) {
	cli := buildSelfHostCLI(t)
	rows := []struct {
		sensor, quality byte
		value           int16
	}{
		{0, 2, -32768}, {0, 2, -4001}, {0, 2, -4000}, {0, 2, -2977},
		{0, 2, -2976}, {15, 3, 12500}, {0, 2, 12501}, {0, 2, 32767},
		{16, 2, 0}, {255, 2, 0}, {0, 4, 0}, {0, 255, 0}, {0, 0, 0}, {0, 1, 0},
	}
	data := make([]byte, len(rows)*8)
	for i, row := range rows {
		data[i*8], data[i*8+1] = row.sensor, row.quality
		binary.LittleEndian.PutUint16(data[i*8+2:], uint16(row.value))
		binary.LittleEndian.PutUint32(data[i*8+4:], 0xffffff00+uint32(i))
	}
	invalid, filtered, accepted, total, low, high, hist, out := etlOracle(data)
	// Pin the reference at both inclusive temperature boundaries and the
	// first histogram boundary, including unsigned sequence bytes.
	if invalid != 8 || filtered != 2 || accepted != 4 || total != 18547 || low != 0 || high != 16500 || hist[0] != 2 || hist[1] != 1 || hist[15] != 1 {
		t.Fatalf("boundary oracle: %d %d %d %d %d %d %v", invalid, filtered, accepted, total, low, high, hist)
	}
	var src strings.Builder
	src.WriteString("import \"./etl_core\";\nimport \"./etl_variants\";\n")
	for n, process := range []string{"etl_core.process", "etl_variants.process_fbip", "etl_variants.process_baseline", "etl_variants.process_batched"} {
		fmt.Fprintf(&src, "function case%d(): i32 {\nlet data: u8[] = %s;\nlet expected: u8[] = %s;\nlet s: etl_core.State = etl_core.new_state(14);\ns = %s(s,data,0,14);\n", n, etlByteLiteral(data), etlByteLiteral(out), process)
		src.WriteString("if (s.seen != 14i64 || s.invalid != 8i64 || s.filtered != 2i64 || s.accepted != 4i64 || s.total != 18547i64 || s.low != 0 || s.high != 16500 || s.output_count != 4) { return 21; }\n")
		fmt.Fprintf(&src, "let saved: etl_core.State = s;\nlet output: u8[] = s.output;\ns = %s(s,data,40,1);\n", process)
		src.WriteString("if (s.accepted != 5i64 || s.total != 35047i64 || s.output_count != 1 || s.output[0] != 116u8) { return 22; }\nif (saved.seen != 14i64 || saved.accepted != 4i64 || saved.total != 18547i64 || saved.output_count != 4) { return 23; }\nlet i: i32 = 0;\nwhile (i < expected.len()) { if (saved.output[i] != expected[i] || output[i] != expected[i]) { return 24; } i = i + 1; }\n")
		for bin, value := range hist {
			fmt.Fprintf(&src, "if (saved.histogram[%d] != %di64) { return 25; }\n", bin, value)
		}
		fmt.Fprintf(&src, "s = %s(s,data,2147483647,1);\ns = %s(s,data,0,2147483647);\nif (s.refused != 2i64 || s.seen != 15i64) { return 26; }\nlet zero: etl_core.State = etl_core.new_state(0);\nzero = %s(zero,data,data.len(),0);\nif (zero.refused != 0i64 || zero.seen != 0i64 || zero.output_count != 0) { return 27; }\nzero = %s(zero,data,0,1);\nif (zero.refused != 1i64 || zero.seen != 0i64) { return 28; }\nreturn 0;\n}\n", process, process, process, process)
	}
	src.WriteString("function main(): i32 {\n")
	for n := 0; n < 4; n++ {
		fmt.Fprintf(&src, "let result%d: i32 = case%d();\nif (result%d != 0) { return result%d; }\n", n, n, n, n)
	}
	src.WriteString("return 0;\n}\n")
	dir := t.TempDir()
	etlCoreForTest(t, dir)
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src.String()), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostFipETLRejectsAllocatingDecode(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, claim := range []string{"fip", "fbip"} {
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(claim+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				etlCoreForTest(t, dir)
				src := fmt.Sprintf("import \"./etl_core\";\n%s function decode(data: u8[], at: i32): etl_core.Record { return etl_core.Record { sensor: data[at] as i32, quality: data[at+1] as i32, value: etl_core.reading(data,at) }; }\nfunction main(): i32 { let data: u8[] = etl_core.dataset(1); return decode(data,0).sensor; }\n", claim)
				path := filepath.Join(dir, "main.fern")
				if err := os.WriteFile(path, []byte(src), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-emit", "asm", "-o", filepath.Join(dir, "out"), path, cli.stdlib)
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "E068") || !strings.Contains(string(out), "un-reused allocation site") {
					t.Fatalf("expected allocation-contract refusal, got %v: %s", err, out)
				}
			})
		}
	}
}

func TestSelfHostFipETLDriver(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	etlCoreForTest(t, dir)
	path := filepath.Join(dir, "etl.fern")
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		for _, variant := range []string{"baseline", "batched", "fbip", "fip"} {
			t.Run(target+"/"+variant, func(t *testing.T) {
				stderr, code := cli.exitOfFileArgs(t, path, target, nil, []string{variant, "257", "64", "samples"}, "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	}
}

func TestSelfHostFipETLDriverArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	etlCoreForTest(t, dir)
	bin := cli.x86Binary(t, filepath.Join(dir, "etl.fern"))
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing", nil},
		{"variant", []string{"unknown", "1", "1"}},
		{"records-text", []string{"fip", "bad", "1"}},
		{"records-zero", []string{"fip", "0", "1"}},
		{"records-negative", []string{"fip", "-1", "1"}},
		{"records-byte-overflow", []string{"fip", "268435456", "1"}},
		{"records-integer-overflow", []string{"fip", "4294967297", "1"}},
		{"batch-text", []string{"fip", "1", "bad"}},
		{"batch-zero", []string{"fip", "1", "0"}},
		{"batch-negative", []string{"fip", "1", "-1"}},
		{"batch-byte-overflow", []string{"fip", "1", "268435456"}},
		{"sample-option", []string{"fip", "1", "1", "unknown"}},
		{"extra", []string{"fip", "1", "1", "samples", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := runX86_64Bin(cli.runner, bin, tc.args...)
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 2 {
				t.Fatalf("want argument rejection (2), got %v: %s", err, out)
			}
		})
	}
}
