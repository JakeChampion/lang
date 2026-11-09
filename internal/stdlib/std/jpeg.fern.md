# JPEG images

This module decodes baseline and progressive JPEG images.

## Model

`decode_image` reads the segments of a JPEG file: the quantization tables, the Huffman tables, the frame and each scan. A scan adds its part of the coefficients of each block: all of them in a baseline file, or one band and some bits of them in a progressive file.

After the last scan, the decoder multiplies the coefficients by the quantization tables. The inverse discrete cosine transform then gives the samples of each block of 8 by 8, and the decoder changes the YCbCr samples to RGB. A chroma component with fewer samples gives each pixel its nearest sample.

## Contracts

The decoder accepts 8-bit Huffman-coded files with one or three components: baseline (SOF0), extended (SOF1) and progressive (SOF2), with any sampling factors and with restart intervals. It refuses lossless and arithmetic-coded files with `Unsupported`. It also refuses with `Unsupported` a frame with more pixels than the limit, before it allocates for the image. `decode_image_limit` takes the limit, and `decode_image` uses `MAX_PIXELS`, the most pixels that the buffers of an image can hold. Data that ends early gives `Truncated`. A scan with corrupt data gives an image with wrong pixels in the blocks after the fault, not an error. A scan that uses a Huffman table that the file does not define is corrupt data in the same way. The pixels are RGBA, and each alpha is 255.

## Executable example

```fern test name=std-jpeg-example
import "std/jpeg";

function main(): i32 {
  match (jpeg.decode_image([0 as u8, 1 as u8])) {
    Ok(_) => {
      return 1;
    },
    Err(e) => {
      if (e.message() != "jpeg: not a JPEG file") {
        return 2;
      }
    }
  }
  return 0;
}
```

## Module assembly

The root chunk keeps the order of the declarations.

```fern
<<*>>=
<<module introduction>>

<<images and errors>>

<<Huffman tables and bits>>

<<segments>>

<<scans>>

<<the pixels>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module decodes baseline and progressive JPEG images.
import "core/cmp";
import "core/error";
import "std/f64";
```

### `images and errors`

```fern
<<images and errors>>=
// An image of `width` by `height` pixels, row by row: red, green, blue and
// alpha for each pixel. A JPEG image is opaque, so each alpha is 255.
pub struct Image { width: i32, height: i32, pixels: u8[] }

pub enum DecodeError { NotJpeg, Unsupported(string), Truncated, Corrupt(string) }

pub function (e: DecodeError) message(): string {
  match (e) {
    NotJpeg => {
      return "jpeg: not a JPEG file";
    },
    Unsupported(what) => {
      return "jpeg: unsupported: " + what;
    },
    Truncated => {
      return "jpeg: the data ends early";
    },
    Corrupt(what) => {
      return "jpeg: corrupt data: " + what;
    }
  }
}

pub function (e: DecodeError) to_string(): string {
  return e.message();
}

impl error.Error for DecodeError {}

impl cmp.Display for DecodeError {}
```

### `Huffman tables and bits`

```fern
<<Huffman tables and bits>>=
// The order of the coefficients of a block in the data: zigzag.
function zigzag(): i32[] {
  return [0, 1, 8, 16, 9, 2, 3, 10, 17, 24, 32, 25, 18, 11, 4, 5, 12, 19, 26, 33, 40, 48, 41, 34, 27, 20, 13, 6, 7, 14, 21, 28, 35, 42, 49, 56, 57, 50, 43, 36, 29, 22, 15, 23, 30, 37, 44, 51, 58, 59, 52, 45, 38, 31, 39, 46, 53, 60, 61, 54, 47, 55, 62, 63];
}

// A Huffman table in canonical form: for each code length, the largest
// code, and the index of the first symbol of the length minus its first
// code.
struct Huffman { maxcode: i32[], offset: i32[], symbols: i32[], ok: boolean }

function no_table(): Huffman {
  return Huffman { maxcode: [], offset: [], symbols: [], ok: false };
}

function huffman(counts: i32[], symbols: i32[]): Huffman {
  let maxcode: i32[] = [];
  let offset: i32[] = [];
  let code = 0;
  let k = 0;
  for len in 0..16 {
    let n = counts[len];
    offset = offset.append(k - code);
    code += n;
    k += n;
    if (n > 0) {
      maxcode = maxcode.append(code - 1);
    } else {
      maxcode = maxcode.append(-1);
    }
    code <<= 1;
  }
  return Huffman { maxcode: maxcode, offset: offset, symbols: symbols, ok: true };
}

// A component of the frame: its sampling factors, its quantization table,
// its size in blocks, and the offset of its blocks, 64 coefficients each,
// in the coefficients of the image.
struct Component { id: i32, h: i32, v: i32, tq: i32, bw: i32, bh: i32, off: i32 }

// The entropy-coded bits: the next byte at `pos`, and the bits not used yet
// in `buf`, the first of them at bit `cnt - 1`.
struct Bits { pos: i32, buf: i32, cnt: i32 }

struct Decoder {
  data: u8[],
  width: i32,
  height: i32,
  progressive: boolean,
  comps: Component[],
  quant: i32[][],
  dc_tables: Huffman[],
  ac_tables: Huffman[],
  restart: i32,
  hmax: i32,
  vmax: i32,
  mcux: i32,
  mcuy: i32,
  frame: boolean,
  max_pixels: i64,
}

function u16_at(b: [u8], at: i32): i32 {
  return b[at] as i32 << 8 | b[at + 1] as i32;
}

// Reads `n` bits of the data, as a number.
function bits(b: [u8], own s: Bits, n: i32): (Bits, i32) {
  let st = s;
  while (st.cnt < n) {
    let byte = 0;
    if (st.pos < b.len()) {
      byte = b[st.pos] as i32;
      if (byte == 255) {
        let next = 0;
        if (st.pos + 1 < b.len()) {
          next = b[st.pos + 1] as i32;
        }
        if (next == 0) {
          st = Bits { pos: st.pos + 2, buf: st.buf, cnt: st.cnt };
        } else {
          // A marker ends the data of the scan: the decoder reads zeros.
          byte = 0;
        }
      } else {
        st = Bits { pos: st.pos + 1, buf: st.buf, cnt: st.cnt };
      }
    }
    st = Bits { pos: st.pos, buf: (st.buf << 8 | byte) & 16777215, cnt: st.cnt + 8 };
  }
  let v = st.buf >> st.cnt - n & (1 << n) - 1;
  return (Bits { pos: st.pos, buf: st.buf, cnt: st.cnt - n }, v);
}

// The value of `n` bits `v` that the data codes by its first bit.
function extend(v: i32, n: i32): i32 {
  if (n == 0) {
    return 0;
  }
  if (v < 1 << n - 1) {
    return v - (1 << n) + 1;
  }
  return v;
}

function decode(b: [u8], own s: Bits, t: Huffman): (Bits, i32) {
  let st = s;
  if (!t.ok) {
    return (st, 0);
  }
  let code = 0;
  for len in 0..16 {
    let (next, bit) = bits(b, st, 1);
    st = next;
    code = code << 1 | bit;
    if (code <= t.maxcode[len]) {
      let i = code + t.offset[len];
      if (i >= 0 && i < t.symbols.len()) {
        return (st, t.symbols[i]);
      }
      return (st, 0);
    }
  }
  return (st, 0);
}
```

### `segments`

```fern
<<segments>>=
// The most pixels that the four bytes of each pixel of an image can hold.
pub const MAX_PIXELS: i64 = 536870911i64;

// The image in the JPEG file `data`, or an error.
pub function decode_image(data: [u8]): Result[Image, DecodeError] {
  return decode_image_limit(data, MAX_PIXELS);
}

// The image in the JPEG file `data`, or an error for a frame of more than
// `max_pixels` pixels.
pub function decode_image_limit(data: [u8], max_pixels: i64): Result[Image, DecodeError] {
  let b = copy(data);
  if (!starts_jpeg(b)) {
    return Err(NotJpeg);
  }
  let d = Decoder { data: b, width: 0, height: 0, progressive: false, comps: [], quant: [[], [], [], []], dc_tables: [no_table(), no_table(), no_table(), no_table()], ac_tables: [no_table(), no_table(), no_table(), no_table()], restart: 0, hmax: 1, vmax: 1, mcux: 0, mcuy: 0, frame: false, max_pixels: max_pixels };
  let coef: i32[] = [];
  let at = next_marker(d.data, 2);
  while (at >= 0) {
    let marker = d.data[at + 1] as i32;
    let seg = at + 4;
    let end = at + 2 + u16_at(d.data, at + 2);
    if (end > d.data.len()) {
      return Err(Truncated);
    }
    if (end < seg) {
      return Err(Corrupt("a segment shorter than its length field"));
    }
    if (marker == 218) {
      if (!d.frame) {
        return Err(Corrupt("a scan before the frame"));
      }
      if let Some(e) = scan_header_fault(d.data, seg, end) {
        return Err(e);
      }
      let (next, after) = scan(d, seg, coef);
      coef = next;
      at = next_marker(d.data, after);
      continue;
    }
    let had_frame = d.frame;
    match (segment(d, marker, seg, end)) {
      Ok(next) => {
        d = next;
      },
      Err(e) => {
        return Err(e);
      }
    }
    if (d.frame && !had_frame) {
      coef = zeros(total_blocks(d) * 64);
    }
    at = next_marker(d.data, end);
  }
  return finish(d, coef);
}

function copy(data: [u8]): u8[] {
  let b: u8[] = [];
  for x in data {
    b = b.append(x);
  }
  return b;
}

// The image of the coefficients, or an error for a file with no frame.
function finish(d: Decoder, coef: [i32]): Result[Image, DecodeError] {
  if (!d.frame) {
    return Err(Corrupt("no frame"));
  }
  return Ok(pixels(d, coef));
}

function starts_jpeg(b: [u8]): boolean {
  return b.len() >= 4 && b[0] == 255u8 && b[1] == 216u8;
}

// A marker with no segment after it: SOI, TEM and the restart markers.
function standalone(marker: i32): boolean {
  return marker == 216 || marker == 1 || marker >= 208 && marker <= 215;
}

// The offset of the next marker from `at` that has a segment, or -1 at the
// end of the image.
function next_marker(b: [u8], at: i32): i32 {
  let p = at;
  while (p + 4 <= b.len()) {
    let marker = b[p + 1] as i32;
    if (b[p] != 255u8 || marker == 255) {
      p += 1;
    } else if (marker == 217) {
      return -1;
    } else if (standalone(marker)) {
      p += 2;
    } else {
      return p;
    }
  }
  return -1;
}

// The frame markers that this module decodes: baseline, extended and
// progressive Huffman coding.
function huffman_frame(marker: i32): boolean {
  return marker == 192 || marker == 193 || marker == 194;
}

// The frame markers of lossless and arithmetic coding.
function other_frame(marker: i32): boolean {
  return marker == 195 || marker >= 197 && marker <= 207 && marker != 200 && marker != 204;
}

// The decoder after the segment of `marker` from `seg` to `end`.
function segment(own d: Decoder, marker: i32, seg: i32, end: i32): Result[Decoder, DecodeError] {
  if (marker == 219) {
    return quant_tables(d, seg, end);
  }
  if (marker == 196) {
    return Ok(huffman_tables(d, seg, end));
  }
  if (marker == 221) {
    if (end - seg < 2) {
      return Err(Truncated);
    }
    let restart = u16_at(d.data, seg);
    return Ok(Decoder { ...d, restart: restart });
  }
  if (huffman_frame(marker)) {
    return frame(d, seg, end, marker == 194);
  }
  if (other_frame(marker)) {
    return Err(Unsupported("lossless or arithmetic coding"));
  }
  return Ok(d);
}

function zeros(n: i32): i32[] {
  let out: i32[] = [];
  for i in 0..n {
    out = out.append(0);
  }
  return out;
}

function total_blocks(d: Decoder): i32 {
  let n = 0;
  for c in d.comps {
    n += c.bw * c.bh;
  }
  return n;
}

function quant_tables(own d: Decoder, seg: i32, end: i32): Result[Decoder, DecodeError] {
  let p = seg;
  let tables = d.quant;
  let zz = zigzag();
  while (p < end) {
    let pq = d.data[p] as i32 >> 4;
    let tq = d.data[p] as i32 & 3;
    p += 1;
    let width = if (pq == 0) { 1 } else { 2 };
    if (p + 64 * width > end) {
      return Err(Truncated);
    }
    let t: i32[] = [];
    for k in 0..64 {
      t = t.append(0);
    }
    for k in 0..64 {
      let v = 0;
      if (pq == 0) {
        v = d.data[p] as i32;
        p += 1;
      } else {
        v = u16_at(d.data, p);
        p += 2;
      }
      t = t.with(zz[k], v);
    }
    tables = tables.with(tq, t);
  }
  return Ok(Decoder { ...d, quant: tables });
}

function huffman_tables(own d: Decoder, seg: i32, end: i32): Decoder {
  let p = seg;
  let dcs = d.dc_tables;
  let acs = d.ac_tables;
  while (p + 17 <= end) {
    let class = d.data[p] as i32 >> 4;
    let id = d.data[p] as i32 & 3;
    let counts: i32[] = [];
    let total = 0;
    for i in 0..16 {
      let n = d.data[p + 1 + i] as i32;
      counts = counts.append(n);
      total += n;
    }
    p += 17;
    let symbols: i32[] = [];
    for i in 0..total {
      if (p + i < end) {
        symbols = symbols.append(d.data[p + i] as i32);
      }
    }
    p += total;
    if (class == 0) {
      dcs = dcs.with(id, huffman(counts, symbols));
    } else {
      acs = acs.with(id, huffman(counts, symbols));
    }
  }
  return Decoder { ...d, dc_tables: dcs, ac_tables: acs };
}

function frame(own d: Decoder, seg: i32, end: i32, progressive: boolean): Result[Decoder, DecodeError] {
  let b = d.data;
  if (d.frame) {
    return Err(Corrupt("a second frame"));
  }
  if (end - seg < 6 || end - seg < 6 + 3 * (b[seg + 5] as i32)) {
    return Err(Truncated);
  }
  if (b[seg] != 8u8) {
    return Err(Unsupported("a precision other than 8 bits"));
  }
  let height = u16_at(b, seg + 1);
  let width = u16_at(b, seg + 3);
  let n = b[seg + 5] as i32;
  if (width <= 0 || height <= 0 || n != 1 && n != 3) {
    return Err(Unsupported("this size or count of components"));
  }
  let (hmax, vmax) = max_sampling(b, seg, n);
  let mcux = (width + 8 * hmax - 1) / (8 * hmax);
  let mcuy = (height + 8 * vmax - 1) / (8 * vmax);
  let Some(comps) = components(b, seg, n, mcux, mcuy) else {
    return Err(Corrupt("a sampling factor of 0"));
  };
  if (width as i64 * (height as i64) > min_i64(d.max_pixels, MAX_PIXELS) || coefficients(comps) > 2147483647i64) {
    return Err(Unsupported("an image of more pixels than the limit"));
  }
  return Ok(Decoder { ...d, width: width, height: height, progressive: progressive, comps: comps, hmax: hmax, vmax: vmax, mcux: mcux, mcuy: mcuy, frame: true });
}

// The count of the coefficients of all the blocks of `comps`, which can be
// more than an i32 holds.
function coefficients(comps: [Component]): i64 {
  let n = 0i64;
  for c in comps {
    n += c.bw as i64 * (c.bh as i64) * 64i64;
  }
  return n;
}

function min_i64(a: i64, b: i64): i64 {
  if (a < b) {
    return a;
  }
  return b;
}

// The largest horizontal and vertical sampling factors of the frame.
function max_sampling(b: [u8], seg: i32, n: i32): (i32, i32) {
  let hmax = 1;
  let vmax = 1;
  for i in 0..n {
    let f = b[seg + 7 + 3 * i] as i32;
    if (f >> 4 > hmax) {
      hmax = f >> 4;
    }
    if ((f & 15) > vmax) {
      vmax = f & 15;
    }
  }
  return (hmax, vmax);
}

// The components of the frame, with the offset of the blocks of each, or
// None when a sampling factor is 0.
function components(b: [u8], seg: i32, n: i32, mcux: i32, mcuy: i32): Option[Component[]] {
  let comps: Component[] = [];
  let off = 0;
  for i in 0..n {
    let c = seg + 6 + 3 * i;
    let h = b[c + 1] as i32 >> 4;
    let v = b[c + 1] as i32 & 15;
    if (h < 1 || v < 1) {
      return None;
    }
    comps = comps.append(Component { id: b[c] as i32, h: h, v: v, tq: b[c + 2] as i32 & 3, bw: mcux * h, bh: mcuy * v, off: off });
    off += mcux * h * mcuy * v * 64;
  }
  return Some(comps);
}
```

### `scans`

```fern
<<scans>>=
// The parameters of a scan: its components, by their index in the frame,
// with their DC and AC tables, the band of coefficients from ss to se, and
// the bit positions ah and al.
struct Scan { comps: i32[], td: i32[], ta: i32[], ss: i32, se: i32, ah: i32, al: i32 }

// Decodes the scan whose header starts at `seg` into `coef`. It gives the
// coefficients and the offset of the marker after the data of the scan.
function scan(d: Decoder, seg: i32, own coef: i32[]): (i32[], i32) {
  let b = d.data;
  let (sc, start) = scan_header(d, seg);
  let st = Bits { pos: start, buf: 0, cnt: 0 };
  let zz = zigzag();
  let c = coef;
  let n = sc.comps.len();
  let dc = zeros(n);
  let eob = 0;
  let (units, ubw) = scan_units(d, sc);
  for u in 0..units {
    if (d.restart > 0 && u > 0 && u % d.restart == 0) {
      st = restart(b, st);
      dc = zeros(n);
      eob = 0;
    }
    for k in 0..n {
      let comp = d.comps[sc.comps[k]];
      let (bx, by, nx, ny) = (u % ubw, u / ubw, 1, 1);
      if (n > 1) {
        bx = u % d.mcux * comp.h;
        by = u / d.mcux * comp.v;
        nx = comp.h;
        ny = comp.v;
      }
      for yy in 0..ny {
        for xx in 0..nx {
          let idx = (by + yy) * comp.bw + bx + xx;
          let (c1, s1, p1, e1) = block(d, sc, k, comp.off + idx * 64, c, st, dc[k], eob, zz);
          c = c1;
          st = s1;
          dc = dc.with(k, p1);
          eob = e1;
        }
      }
    }
  }
  return (c, data_end(b, st.pos));
}

// The parameters of the scan whose header starts at `seg`, and the offset
// of its data.
// The fault of the scan header at `seg`, a segment that ends at `end`: a
// header longer than its segment, a table selector past the four tables of
// each class, or a spectral selection outside the 64 coefficients.
function scan_header_fault(b: [u8], seg: i32, end: i32): Option[DecodeError] {
  if (end - seg < 1) {
    return Some(Truncated);
  }
  let n = b[seg] as i32;
  if (end - seg < 1 + 2 * n + 3) {
    return Some(Truncated);
  }
  for i in 0..n {
    let tables = b[seg + 2 + 2 * i] as i32;
    if (tables >> 4 > 3 || (tables & 15) > 3) {
      return Some(Corrupt("a Huffman table selector past 3"));
    }
  }
  let ss = b[seg + 1 + 2 * n] as i32;
  let se = b[seg + 2 + 2 * n] as i32;
  if (ss > 63 || se > 63 || ss > se) {
    return Some(Corrupt("a spectral selection outside 0 to 63"));
  }
  return None;
}

function scan_header(d: Decoder, seg: i32): (Scan, i32) {
  let b = d.data;
  let n = b[seg] as i32;
  let ids: i32[] = [];
  let tds: i32[] = [];
  let tas: i32[] = [];
  for i in 0..n {
    let id = b[seg + 1 + 2 * i] as i32;
    let tables = b[seg + 2 + 2 * i] as i32;
    for k in 0..d.comps.len() {
      if (d.comps[k].id == id) {
        ids = ids.append(k);
        tds = tds.append(tables >> 4);
        tas = tas.append(tables & 15);
      }
    }
  }
  let p = seg + 1 + 2 * n;
  return (Scan { comps: ids, td: tds, ta: tas, ss: b[p] as i32, se: b[p + 1] as i32, ah: b[p + 2] as i32 >> 4, al: b[p + 2] as i32 & 15 }, p + 3);
}

// The units of a scan, and the width of a row of them in blocks. A scan of
// one component has its blocks in order, as far as the image reaches. A
// scan of more has interleaved MCUs, each with the h by v blocks of each
// component.
function scan_units(d: Decoder, sc: Scan): (i32, i32) {
  if (sc.comps.len() != 1) {
    return (d.mcux * d.mcuy, d.mcux);
  }
  let comp = d.comps[sc.comps[0]];
  let ubw = ((d.width * comp.h + d.hmax - 1) / d.hmax + 7) / 8;
  let ubh = ((d.height * comp.v + d.vmax - 1) / d.vmax + 7) / 8;
  return (ubw * ubh, ubw);
}

// The offset of the next marker from `at` that is not a restart marker:
// the end of the data of a scan.
function data_end(b: [u8], at: i32): i32 {
  let q = at;
  while (q + 1 < b.len() && !(b[q] == 255u8 && b[q + 1] != 0u8 && !(b[q + 1] >= 208u8 && b[q + 1] <= 215u8))) {
    q += 1;
  }
  return q;
}

// The bits after the next restart marker.
function restart(b: [u8], s: Bits): Bits {
  let q = s.pos;
  while (q + 1 < b.len() && !(b[q] == 255u8 && b[q + 1] >= 208u8 && b[q + 1] <= 215u8)) {
    q += 1;
  }
  return Bits { pos: q + 2, buf: 0, cnt: 0 };
}

// Decodes the part of the block at `base` that the scan holds, for the
// `k`th component of the scan. It gives the coefficients, the bits, the DC
// prediction and the end-of-band run.
function block(d: Decoder, sc: Scan, k: i32, base: i32, own coef: i32[], own s: Bits, pred: i32, eob: i32, zz: i32[]): (i32[], Bits, i32, i32) {
  let b = d.data;
  if (base < 0 || base + 64 > coef.len()) {
    return (coef, s, pred, eob);
  }
  let st = s;
  let c = coef;
  let dc = pred;
  let dct = d.dc_tables[sc.td[k]];
  let act = d.ac_tables[sc.ta[k]];
  if (sc.ss == 0) {
    if (sc.ah == 0) {
      let (s1, t) = decode(b, st, dct);
      let (s2, v) = bits(b, s1, t);
      st = s2;
      dc += extend(v, t);
      c = c.with(base, dc << sc.al);
    } else {
      let (s1, bit) = bits(b, st, 1);
      st = s1;
      if (bit != 0) {
        let old = c[base];
        c = c.with(base, old | 1 << sc.al);
      }
    }
    if (!d.progressive) {
      let (s3, band) = ac_first(b, st, act, c, base, 1, 63, 0, 0, zz);
      return (band.coef, s3, dc, 0);
    }
    if (sc.se == 0) {
      return (c, st, dc, eob);
    }
  }
  let ss = sc.ss;
  if (ss == 0) {
    ss = 1;
  }
  if (sc.ah == 0) {
    let (s4, band) = ac_first(b, st, act, c, base, ss, sc.se, sc.al, eob, zz);
    return (band.coef, s4, dc, band.eobrun);
  }
  let (s5, band) = ac_refine(b, st, act, c, base, ss, sc.se, sc.al, eob, zz);
  return (band.coef, s5, dc, band.eobrun);
}

struct Band { coef: i32[], eobrun: i32 }

// The first bits of the AC coefficients from ss to se, at bit `al`.
function ac_first(b: [u8], own s: Bits, t: Huffman, own coef: i32[], base: i32, ss: i32, se: i32, al: i32, eobrun: i32, zz: i32[]): (Bits, Band) {
  if (eobrun > 0) {
    return (s, Band { coef: coef, eobrun: eobrun - 1 });
  }
  let st = s;
  let c = coef;
  let k = ss;
  let run = 0;
  while (k <= se) {
    let (s1, rs) = decode(b, st, t);
    st = s1;
    let r = rs >> 4;
    let n = rs & 15;
    if (n == 0) {
      if (r < 15) {
        run = (1 << r) - 1;
        if (r > 0) {
          let (s2, extra) = bits(b, st, r);
          st = s2;
          run += extra;
        }
        break;
      }
      k += 16;
      continue;
    }
    k += r;
    let (s3, v) = bits(b, st, n);
    st = s3;
    if (k <= 63) {
      c = c.with(base + zz[k], extend(v, n) << al);
    }
    k += 1;
  }
  return (st, Band { coef: c, eobrun: run });
}

// Adds one bit at `al` to a coefficient that is not zero.
function refine(b: [u8], own s: Bits, own coef: i32[], at: i32, al: i32): (Bits, i32[]) {
  let (s1, bit) = bits(b, s, 1);
  let v = coef[at];
  if (bit != 0 && (v & 1 << al) == 0) {
    if (v >= 0) {
      return (s1, coef.with(at, v + (1 << al)));
    }
    return (s1, coef.with(at, v - (1 << al)));
  }
  return (s1, coef);
}

// The next bit of each AC coefficient from ss to se: a bit for each that is
// not zero yet, and the new coefficients of 1 or -1 at bit `al`.
function ac_refine(b: [u8], own s: Bits, t: Huffman, own coef: i32[], base: i32, ss: i32, se: i32, al: i32, eobrun: i32, zz: i32[]): (Bits, Band) {
  let st = s;
  let c = coef;
  let k = ss;
  let run = eobrun;
  if (run == 0) {
    while (k <= se) {
      let (s1, rs) = decode(b, st, t);
      st = s1;
      let r = rs >> 4;
      let n = rs & 15;
      let value = 0;
      if (n != 0) {
        let (s2, bit) = bits(b, st, 1);
        st = s2;
        value = 2 * bit - 1 << al;
      } else if (r != 15) {
        let (s3, extra) = bits(b, st, r);
        st = s3;
        run = (1 << r) + extra;
        break;
      }
      let (s4, c4, at) = advance(b, st, c, base, k, se, r, al, zz);
      st = s4;
      c = c4;
      k = at;
      if (value != 0 && k <= se) {
        c = c.with(base + zz[k], value);
      }
      k += 1;
    }
  }
  if (run > 0) {
    let (s5, c5, at) = advance(b, st, c, base, k, se, 64, al, zz);
    return (s5, Band { coef: c5, eobrun: run - 1 });
  }
  return (st, Band { coef: c, eobrun: run });
}

// Refines each coefficient from k that is not zero, and passes `r` zero
// coefficients. It stops at the zero coefficient after them, or after se.
// It gives the bits, the coefficients and the place where it stopped.
function advance(b: [u8], own s: Bits, own coef: i32[], base: i32, k0: i32, se: i32, r0: i32, al: i32, zz: i32[]): (Bits, i32[], i32) {
  let st = s;
  let c = coef;
  let k = k0;
  let r = r0;
  while (k <= se) {
    let at = base + zz[k];
    if (c[at] != 0) {
      let (s1, nc) = refine(b, st, c, at, al);
      st = s1;
      c = nc;
    } else if (r == 0) {
      return (st, c, k);
    } else {
      r -= 1;
    }
    k += 1;
  }
  return (st, c, k);
}
```

### `the pixels`

```fern
<<the pixels>>=
// cos((2x + 1) u pi / 16), scaled by c(u) / 2, for x and u from 0 to 7.
function idct_table(): f64[] {
  let out: f64[] = [];
  for x in 0..8 {
    for u in 0..8 {
      let cu = 0.5;
      if (u == 0) {
        cu = 0.5 / 2.0.sqrt();
      }
      out = out.append(cu * ((2 * x + 1) as f64 * (u as f64) * 3.14159265358979 / 16.0).cos());
    }
  }
  return out;
}

// The 64 samples of the block at `base`, after dequantizing with `q`.
function idct(coef: [i32], base: i32, q: [i32], t: [f64]): u8[] {
  return idct_columns(idct_rows(coef, base, q, t), t);
}

// The transform of each row of frequencies v: the samples x.
function idct_rows(coef: [i32], base: i32, q: [i32], t: [f64]): f64[] {
  let tmp: f64[] = [];
  for v in 0..8 {
    for x in 0..8 {
      let sum = 0.0;
      for u in 0..8 {
        let k = v * 8 + u;
        let cv = coef[base + k];
        if (cv != 0) {
          sum += (cv * q[k]) as f64 * t[x * 8 + u];
        }
      }
      tmp = tmp.append(sum);
    }
  }
  return tmp;
}

// The transform of each column of `tmp`, shifted by 128 into 0 to 255.
function idct_columns(tmp: [f64], t: [f64]): u8[] {
  let out: u8[] = [];
  for y in 0..8 {
    for x in 0..8 {
      let sum = 128.5;
      for v in 0..8 {
        sum += tmp[v * 8 + x] * t[y * 8 + v];
      }
      out = out.append(clamp255(sum.floor() as i32));
    }
  }
  return out;
}

// The samples of component `c`, at its own size in blocks times 8.
function plane(c: Component, coef: [i32], q: [i32], t: [f64]): u8[] {
  let w = c.bw * 8;
  let out: u8[] = [];
  for i in 0..w * c.bh * 8 {
    out = out.append(0u8);
  }
  for by in 0..c.bh {
    for bx in 0..c.bw {
      let s = idct(coef, c.off + (by * c.bw + bx) * 64, q, t);
      for y in 0..8 {
        let row = (by * 8 + y) * w + bx * 8;
        for x in 0..8 {
          out = out.with(row + x, s[y * 8 + x]);
        }
      }
    }
  }
  return out;
}

function clamp255(v: i32): u8 {
  if (v < 0) {
    return 0u8;
  }
  if (v > 255) {
    return 255u8;
  }
  return v as u8;
}

function pixels(d: Decoder, coef: [i32]): Image {
  let t = idct_table();
  let planes: u8[][] = [];
  for c in d.comps {
    let q = d.quant[c.tq];
    if (q.len() < 64) {
      q = [1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1];
    }
    planes = planes.append(plane(c, coef, q, t));
  }
  let out: u8[] = [];
  let gray = d.comps.len() == 1;
  for y in 0..d.height {
    for x in 0..d.width {
      let samples: i32[] = [];
      for k in 0..d.comps.len() {
        let c = d.comps[k];
        let sx = x * c.h / d.hmax;
        let sy = y * c.v / d.vmax;
        samples = samples.append(planes[k][sy * c.bw * 8 + sx] as i32);
      }
      if (gray) {
        let g = samples[0] as u8;
        out = out.append(g).append(g).append(g).append(255u8);
      } else {
        let yy = samples[0] as f64;
        let cb = samples[1] as f64 - 128.0;
        let cr = samples[2] as f64 - 128.0;
        out = out.append(clamp255((yy + 1.402 * cr + 0.5).floor() as i32));
        out = out.append(clamp255((yy - 0.344136 * cb - 0.714136 * cr + 0.5).floor() as i32));
        out = out.append(clamp255((yy + 1.772 * cb + 0.5).floor() as i32));
        out = out.append(255u8);
      }
    }
  }
  return Image { width: d.width, height: d.height, pixels: out };
}
```
