package e2eharness

func BufferedWriterBytesOutput() []byte {
	out := []byte("directé")
	for i := range 256 {
		out = append(out, byte(i))
	}
	out = append(out, 0xc3, 0xff, 'a', 0, 0x80, 0xff, 'a')
	return append(out, []byte("tail")...)
}

const BufferedWriterBytesProgram = `import "std/io_buffered" as io;
function main(): i32 {
  var b = io.buf_writer_new(stdout(), 4);
  b = b.write_string("directé");
  var i: i32 = 0;
  while (i < 256) { b = b.write_byte(i); i = i + 1; }
  b = b.write_range("é", 0, 1);
  b = b.flush();
  b = b.write_mapped("\x00a", [255 as u8]);
  b = b.write_expanded("\x00", [3 as u8, 0 as u8, 128 as u8, 255 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8]);
  b = b.write_filtered("\x00a", [1 as u8]);
  b = b.flush();
  b = b.write_string("tail");
  b = b.flush();
  b = b.flush();
  if (b.buffered() != 0) { return 1; }
  match (b.error()) { Some(_) => { return 2; }, None => {} }
  buf_free(b.handle());
  // An existing error discards pending bytes but survives each flush.
  var failed = io.BufWriter { w: stdout(), buf: buf_new(1), cap: 1, err: Some(Other("first", "failure")) };
  failed = failed.write_byte(255);
  failed = failed.write_string("discarded");
  if (failed.buffered() != 0) { return 3; }
  match (failed.error()) {
    Some(Other(path, message)) => { if (path != "first" || message != "failure") { return 4; } },
    _ => { return 5; }
  }
  buf_free(failed.handle());
  return 0;
}
`
