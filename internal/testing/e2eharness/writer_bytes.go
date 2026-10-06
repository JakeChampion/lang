package e2eharness

func WriterBytesOutput() []byte {
	out := make([]byte, 16387)
	for i := 0; i < 16384; i++ {
		out[i] = byte(i)
	}
	copy(out[16384:], []byte{0, 255, 128})
	for i := 17; i < 273; i++ {
		out = append(out, byte(i))
	}
	out = append(out, []byte("A\x00€Z€\x82\xacXYZ")...)
	return out
}

const WriterBytesProgram = `import "std/array";
import "std/string";
@noinline function joined_text(a: string, b: string): string { return a + b; }
function write_view(w: Writer, data: [u8]): i32 {
 match (w.write_bytes(data)) { Some(_) => { return 1; }, None => {} }
 return 0;
}
function main(): i32 {
 let data: u8[] = [];
 let i: i32 = 0;
 while (i < 8192) { data = data.append((i % 256) as u8); i = i + 1; }
 let original = data;
 let w = stdout();
 match (w.write("")) { Some(_) => { return 17; }, None => { } }
 match (w.write_some("")) { Ok(n) => { if (n != 0) { return 18; } }, Err(_) => { return 19; } }
 match (w.write_bytes(data)) { Some(_) => { return 1; }, None => { } }
 match (w.write_some_bytes(data)) {
  Err(_) => { return 14; },
  Ok(n) => {
   if (n <= 0 || n > 8192) { return 15; }
   let tail = data.drop(n as i32);
   match (w.write_bytes(tail)) { Some(_) => { return 16; }, None => { } }
  }
 }
 let small: u8[] = [0 as u8, 255 as u8, 128 as u8];
 match (w.write_some_bytes(small)) { Ok(n) => { if (n != 3) { return 2; } }, Err(_) => { return 3; } }
 match (w.write_some_bytes([])) { Ok(n) => { if (n != 0) { return 4; } }, Err(_) => { return 5; } }
 match (w.write_bytes([])) { Some(_) => { return 6; }, None => { } }
 if (data.len() != original.len() || small.len() != 3 || small[0] != 0 || small[1] != 255 || small[2] != 128) { return 7; }
 i = 0;
 while (i < data.len()) { if (data[i] != original[i]) { return 7; } i = i + 1; }
 let changed = data.with(0, 255 as u8);
 if (original[0] != 0 || changed[0] != 255) { return 8; }
 let sub: [u8] = data[17:273];
 if (write_view(w, sub) != 0) { return 20; }
 let text: string = joined_text("A\0", "€Z");
 let held: string = text;
 let view: [u8] = text.as_bytes();
 if (write_view(w, view) != 0) { return 21; }
 let middle: str = slice_unchecked(text, 2, 5);
 match (w.write_some_bytes(middle.as_bytes())) { Ok(n) => { if (n != 3) { return 22; } }, Err(_) => { return 23; } }
 if (write_view(w, view[3:5]) != 0) { return 24; }
 if (write_view(w, "X".as_bytes()) != 0) { return 31; }
 if (write_view(w, joined_text("Y", "Z").as_bytes()) != 0) { return 32; }
 if (held != "A\0€Z" || sub[0] != 17 as u8 || sub[255] != 16 as u8 || view.len() != 6) { return 25; }
 match (w.write_bytes("".as_bytes())) { Some(_) => { return 26; }, None => {} }
 match (w.write_some_bytes(view[2:2])) { Ok(n) => { if (n != 0) { return 27; } }, Err(_) => { return 28; } }
 let closed = stderr();
 match (closed.close()) { Some(_) => { return 9; }, None => { } }
 match (closed.write_bytes(small)) { Some(_) => { }, None => { return 10; } }
 match (closed.write_bytes([])) { Some(_) => { }, None => { return 11; } }
 match (closed.write_some_bytes(small)) { Err(_) => { }, Ok(_) => { return 12; } }
 match (closed.write_some_bytes([])) { Err(_) => { }, Ok(_) => { return 13; } }
 match (closed.write_bytes(view)) { Some(_) => {}, None => { return 29; } }
 match (closed.write_some_bytes("".as_bytes())) { Err(_) => {}, Ok(_) => { return 30; } }
 return 0;
}
`
