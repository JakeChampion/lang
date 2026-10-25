package e2eharness

func BufferedWriterBytesOutput() []byte {
	out := []byte("directé")
	for i := range 256 {
		out = append(out, byte(i))
	}
	out = append(out, 0xc3, 0xff, 'a', 0, 0x80, 0xff, 'a')
	out = append(out, []byte("tail")...)
	out = append(out, 255, 0, 128, 254, 193, 255, 0, 254, 255, 0, 128, 254)
	out = append(out, 255, 193, 128, 254, 255, 128, 254, 0, 128, 255, 255)
	for range 64 {
		out = append(out, 255, 0, 128, 0, 255, 0, 128, 255, 128, 255, 128)
	}
	return out
}

const BufferedWriterBytesProgram = `import "std/io_buffered" as io;
function run(): Result[i32, IoError] {
  let b = io.buf_writer_new(stdout(), 4);
  b = b.write_string("directé")?;
  let i: i32 = 0;
  while (i < 256) { b = b.write_byte(i)?; i = i + 1; }
  b = b.write_range("é", 0, 1)?;
  b = b.flush()?;
  b = b.write_mapped("\x00a", [255 as u8])?;
  b = b.write_expanded("\x00", [3 as u8, 0 as u8, 128 as u8, 255 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8])?;
  b = b.write_filtered("\x00a", [1 as u8])?;
  b = b.flush()?;
  b = b.write_string("tail")?;
  b = b.flush()?;
  let raw: u8[] = [255 as u8, 0 as u8, 128 as u8, 254 as u8];
  let held = raw;
  b = b.write_bytes(raw)?;
  b = b.write_bytes([193 as u8])?;
  b = b.write_bytes_range(raw, 0 - 2, 2)?;
  b = b.write_bytes_range(raw, 3, 99)?;
  b = b.write_bytes_range(raw, 2, 1)?;
  b = b.write_bytes_range(raw, 0 - 5, 99)?;
  b = b.write_bytes_mapped(raw, [193 as u8])?;
  b = b.write_bytes_filtered(raw, [2 as u8])?;
  b = b.write_bytes_expanded([0 as u8, 255 as u8], [3 as u8, 0 as u8, 128 as u8, 255 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8] )?;
  raw = raw.with(0, 1 as u8);
  if (held[0] != 255 as u8 || raw[0] != 1 as u8) { return Ok(6); }
  b = b.flush()?;
  if (b.buffered() != 0) { return Ok(1); }
  match (b.error()) { Some(_) => { return Ok(2); }, None => {} }
  // Fresh builder results must be released after the writer borrows them.
  // Exercise direct writes, buffered writes, ranges, and empty arrays.
  let seed = buf_new(3);
  for capacity in [1, 64] {
    let fresh = io.buf_writer_new(stdout(), capacity);
    let alias = fresh;
    for iteration in 0..32 {
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      fresh = fresh.write_bytes(buf_take_bytes(seed))?;
      if (capacity == 64 && alias.buffered() != 3) { return Ok(7); }
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      fresh = fresh.write_bytes_range(buf_take_bytes(seed), 1, 2)?;
      fresh = fresh.write_bytes(buf_take_bytes(seed))?;
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      fresh = fresh.write_bytes_mapped(buf_take_bytes(seed), buf_take_bytes(seed))?;
      buf_push_byte(seed, 255); buf_push_byte(seed, 0); buf_push_byte(seed, 128);
      fresh = fresh.write_bytes_filtered(buf_take_bytes(seed), [1 as u8])?;
      buf_push_byte(seed, 0);
      fresh = fresh.write_bytes_expanded(buf_take_bytes(seed), [2 as u8, 255 as u8, 128 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8])?;
      fresh = fresh.flush()?;
      if (alias.buffered() != 0) { return Ok(8); }
    }
  }
  // An existing error is answered by every write that reaches the Writer.
  let failed = io.buf_writer_new(stdout(), 1);
  failed.err.set(Some(Other("first", "failure", 0)));
  if (!first_or_ok(failed.write_byte(255))) { return Ok(9); }
  if (!first_or_ok(failed.write_string("discarded"))) { return Ok(9); }
  if (!first_or_ok(failed.write_bytes(held))) { return Ok(9); }
  if (!first_or_ok(failed.write_bytes_range(held, 1, 3))) { return Ok(9); }
  if (!first_or_ok(failed.write_bytes_mapped(held, []))) { return Ok(9); }
  if (!first_or_ok(failed.write_bytes_filtered(held, []))) { return Ok(9); }
  if (!first_or_ok(failed.write_bytes_expanded(held, []))) { return Ok(9); }
  buf_push_byte(seed, 255);
  if (!first_or_ok(failed.write_bytes(buf_take_bytes(seed)))) { return Ok(9); }
  buf_push_byte(seed, 255);
  if (!first_or_ok(failed.write_bytes_range(buf_take_bytes(seed), 0, 1))) { return Ok(9); }
  buf_push_byte(seed, 255);
  if (!first_or_ok(failed.write_bytes_mapped(buf_take_bytes(seed), buf_take_bytes(seed)))) { return Ok(9); }
  buf_push_byte(seed, 255);
  if (!first_or_ok(failed.write_bytes_filtered(buf_take_bytes(seed), buf_take_bytes(seed)))) { return Ok(9); }
  buf_push_byte(seed, 255);
  if (!first_or_ok(failed.write_bytes_expanded(buf_take_bytes(seed), buf_take_bytes(seed)))) { return Ok(9); }
  match (failed.flush()) {
    Err(Other(path, message, _)) => { if (path != "first" || message != "failure") { return Ok(10); } },
    _ => { return Ok(10); }
  }
  buf_free(seed);
  if (failed.buffered() != 0) { return Ok(3); }
  match (failed.error()) {
    Some(Other(path, message, _)) => { if (path != "first" || message != "failure") { return Ok(4); } },
    _ => { return Ok(5); }
  }
  return Ok(0);
}

function first_or_ok(r: Result[io.BufWriter, IoError]): boolean {
  match (r) {
    Ok(_) => { return true; },
    Err(Other(path, message, _)) => { return path == "first" && message == "failure"; },
    Err(_) => { return false; }
  }
  return false;
}

function main(): i32 {
  match (run()) {
    Ok(code) => { return code; },
    Err(_) => { return 99; }
  }
}
`
