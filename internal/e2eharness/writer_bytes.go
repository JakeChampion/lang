package e2eharness

func WriterBytesOutput() []byte {
	out := make([]byte, 16387)
	for i := 0; i < 16384; i++ {
		out[i] = byte(i)
	}
	copy(out[16384:], []byte{0, 255, 128})
	return out
}

const WriterBytesProgram = `import "std/array";
function main(): i32 {
 var data: u8[] = [];
 var i: i32 = 0;
 while (i < 8192) { data = data.append((i % 256) as u8); i = i + 1; }
 var original = data;
 var w = stdout();
 match (w.write("")) { Some(_) => { return 17; }, None => { } }
 match (w.write_some("")) { Ok(n) => { if (n != 0) { return 18; } }, Err(_) => { return 19; } }
 match (w.write_bytes(data)) { Some(_) => { return 1; }, None => { } }
 match (w.write_some_bytes(data)) {
  Err(_) => { return 14; },
  Ok(n) => {
   if (n <= 0 || n > 8192) { return 15; }
   var tail = data.drop(n as i32);
   match (w.write_bytes(tail)) { Some(_) => { return 16; }, None => { } }
  }
 }
 var small: u8[] = [0 as u8, 255 as u8, 128 as u8];
 match (w.write_some_bytes(small)) { Ok(n) => { if (n != 3) { return 2; } }, Err(_) => { return 3; } }
 match (w.write_some_bytes([])) { Ok(n) => { if (n != 0) { return 4; } }, Err(_) => { return 5; } }
 match (w.write_bytes([])) { Some(_) => { return 6; }, None => { } }
 if (data.len() != original.len() || small.len() != 3 || small[0] != 0 || small[1] != 255 || small[2] != 128) { return 7; }
 i = 0;
 while (i < data.len()) { if (data[i] != original[i]) { return 7; } i = i + 1; }
 var changed = data.with(0, 255 as u8);
 if (original[0] != 0 || changed[0] != 255) { return 8; }
 var closed = stderr();
 match (closed.close()) { Some(_) => { return 9; }, None => { } }
 match (closed.write_bytes(small)) { Some(_) => { }, None => { return 10; } }
 match (closed.write_bytes([])) { Some(_) => { }, None => { return 11; } }
 match (closed.write_some_bytes(small)) { Err(_) => { }, Ok(_) => { return 12; } }
 match (closed.write_some_bytes([])) { Err(_) => { }, Ok(_) => { return 13; } }
 return 0;
}
`
