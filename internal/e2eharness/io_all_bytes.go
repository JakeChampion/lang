package e2eharness

import "strings"

func IOAllBytesInput() []byte {
	b := make([]byte, 8448)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// IOAllBytesProgram checks complete binary collection and that borrowing a
// reader leaves its handle open. The closed-handle call exercises cleanup.
const IOAllBytesProgram = `import "std/io";
function main(): i32 {
    var r: Reader = stdin();
    match (io.read_all_bytes(r)) {
        Err(_) => { return 1; },
        Ok(bytes) => {
            if (bytes.len() != 8448) { return 2; }
            match (r.read_chunk_bytes(1)) {
                Err(_) => { return 3; },
                Ok(end) => { if (end.len() != 0) { return 4; } },
            }
            match (r.close()) { Some(_) => { return 5; }, None => {} }
            match (io.read_all_bytes(r)) { Ok(_) => { return 6; }, Err(_) => {} }
            var i: i32 = 0;
            while (i < bytes.len()) {
                if (bytes[i] != (i % 256) as u8) { return 7; }
                i = i + 1;
            }
        },
    }
    return 0;
}
`

func IOStdinBytesProgram(call string, empty bool) string {
	check := `if (bytes.len() != 8448) { return 2; }
            var i: i32 = 0;
            while (i < bytes.len()) {
                if (bytes[i] != (i % 256) as u8) { return 3; }
                i = i + 1;
            }`
	if empty {
		check = "if (bytes.len() != 0) { return 2; }"
	}
	return strings.ReplaceAll(strings.ReplaceAll(`import "std/io";
function main(): i32 {
    match (CALL) {
        Err(_) => { return 1; },
        Ok(bytes) => { CHECK },
    }
    return 0;
}
`, "CALL", call), "CHECK", check)
}
