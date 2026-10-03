package e2eharness

func BufferedWriterBytesOutput() []byte {
	out := []byte("directé")
	for i := range 256 {
		out = append(out, byte(i))
	}
	out = append(out, 0xc3, 0xff, 'a', 0, 0x80, 0xff, 'a')
	out = append(out, []byte("tail")...)
	out = append(out, 255, 0, 128, 254, 193, 255, 0, 254, 255, 0, 128, 254)
	for range 64 {
		out = append(out, 255, 0, 128, 0)
	}
	return out
}

const BufferedWriterBytesProgram = `import "std/io_buffered" as io;
function main(): i32 {
  let b = io.buf_writer_new(stdout(), 4);
  b = b.write_string("directé");
  let i: i32 = 0;
  while (i < 256) { b = b.write_byte(i); i = i + 1; }
  b = b.write_range("é", 0, 1);
  b = b.flush();
  b = b.write_mapped("\x00a", [255 as u8]);
  b = b.write_expanded("\x00", [3 as u8, 0 as u8, 128 as u8, 255 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8]);
  b = b.write_filtered("\x00a", [1 as u8]);
  b = b.flush();
  b = b.write_string("tail");
  b = b.flush();
  let raw: u8[] = [255 as u8, 0 as u8, 128 as u8, 254 as u8];
  let held = raw;
  b = b.write_bytes(raw);
  b = b.write_bytes([193 as u8]);
  b = b.write_bytes_range(raw, 0 - 2, 2);
  b = b.write_bytes_range(raw, 3, 99);
  b = b.write_bytes_range(raw, 2, 1);
  b = b.write_bytes_range(raw, 0 - 5, 99);
  raw = raw.with(0, 1 as u8);
  if (held[0] != 255 as u8 || raw[0] != 1 as u8) { return 6; }
  b = b.flush();
  if (b.buffered() != 0) { return 1; }
  match (b.error()) { Some(_) => { return 2; }, None => {} }
  // Fresh builder results must be released after the writer borrows them.
  // Exercise direct writes, buffered writes, ranges, and empty arrays.
  let seed = buf_new(3);
  for capacity in [1, 64] {
    let fresh = io.buf_writer_new(stdout(), capacity);
    let alias = fresh;
    for iteration in 0..32 {
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      fresh = fresh.write_bytes(buf_take_bytes(seed));
      if (capacity == 64 && alias.buffered() != 3) { return 7; }
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      fresh = fresh.write_bytes_range(buf_take_bytes(seed), 1, 2);
      fresh = fresh.write_bytes(buf_take_bytes(seed));
      fresh = fresh.flush();
      if (alias.buffered() != 0) { return 8; }
    }
  }
  // An existing error discards pending bytes but survives each flush.
  let failed = io.BufWriter { w: stdout(), buf: io.BufBlock { h: buf_new(1) }, cap: 1, err: Some(Other("first", "failure")) };
  failed = failed.write_byte(255);
  failed = failed.write_string("discarded");
  failed = failed.write_bytes(held);
  failed = failed.write_bytes_range(held, 1, 3);
  buf_push_byte(seed, 255);
  failed = failed.write_bytes(buf_take_bytes(seed));
  buf_push_byte(seed, 255);
  failed = failed.write_bytes_range(buf_take_bytes(seed), 0, 1);
  buf_free(seed);
  if (failed.buffered() != 0) { return 3; }
  match (failed.error()) {
    Some(Other(path, message)) => { if (path != "first" || message != "failure") { return 4; } },
    _ => { return 5; }
  }
  return 0;
}
`
