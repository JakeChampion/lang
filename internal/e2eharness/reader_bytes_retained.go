package e2eharness

import "fmt"

// ReaderBytesRetainedProgram keeps every chunk alive while later reads reuse
// scratch storage. slotBytes describes the target array representation.
func ReaderBytesRetainedProgram(request, slotBytes, reads int, sanitize bool) string {
	bound := fmt.Sprintf("if (growth > %d as i64 * 4 as i64 * 65536 as i64) { return 6; }", slotBytes)
	if sanitize {
		// Quarantine intentionally prevents scratch reuse.
		bound = ""
	}
	return fmt.Sprintf(`function main(): i32 {
  var r: Reader = match (open_reader("input")) {
    Ok(reader) => { reader }, Err(_) => { return 1; }
  };
  var chunks: u8[][] = [];
  var before = __heap_bump_bytes();
  for i in 0..%d {
    match (r.seek(0 as i64, 0)) { Ok(_) => {}, Err(_) => { return 2; } }
    match (r.read_chunk_bytes(%d)) {
      Ok(chunk) => { chunks = chunks.append(chunk); },
      Err(_) => { return 3; }
    }
  }
  var growth = __heap_bump_bytes() - before;
  for chunk in chunks {
    if (chunk.len() != 1024) { return 4; }
    for j in 0..1024 { if (chunk[j] != (j %% 256) as u8) { return 5; } }
  }
  // The held payload totals 64 KiB. Allow another three request-sized blocks
  // for reusable scratch, allocator rounding, metadata and ownership boxes.
  %s
  match (r.seek(0 as i64, -1)) { Ok(_) => { return 8; }, Err(_) => {} }
  match (r.close()) { None => {}, Some(_) => { return 7; } }
  match (r.seek(0 as i64, 0)) { Ok(_) => { return 9; }, Err(_) => {} }
  return 0;
}`, reads, request, bound)
}
