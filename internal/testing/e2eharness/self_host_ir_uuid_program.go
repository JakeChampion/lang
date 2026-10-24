package e2eharness

const UuidV4Program = `
function hexd(n: i32): string { return slice_unchecked("0123456789abcdef", n, n + 1) + ""; }
function bh(b: i32): string { return hexd((b >> 4) & 15) + hexd(b & 15); }
function v4(): string {
  let b: u8[] = random_bytes(16);
  let b6: i32 = ((b[6] as i32) & 15) | 64;
  let b8: i32 = ((b[8] as i32) & 63) | 128;
  return bh((b[0] as i32)) + bh((b[1] as i32)) + bh((b[2] as i32)) + bh((b[3] as i32)) + "-" + bh((b[4] as i32)) + bh((b[5] as i32)) + "-" + bh(b6) + bh((b[7] as i32)) + "-" + bh(b8) + bh((b[9] as i32)) + "-" + bh((b[10] as i32)) + bh((b[11] as i32)) + bh((b[12] as i32)) + bh((b[13] as i32)) + bh((b[14] as i32)) + bh((b[15] as i32));
}
function main(): i32 {
  let u: string = v4();
  if (u.len() != 36) { return 10; }
  if (u[14] != 52) { return 11; }
  if (u[8] != 45) { return 12; }
  if (u[13] != 45) { return 13; }
  if (u[23] != 45) { return 14; }
  if (v4() == v4()) { return 15; }
  return 0;
}
`
