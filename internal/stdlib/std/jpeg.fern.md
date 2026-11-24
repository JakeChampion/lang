# JPEG images

This module decodes baseline and progressive JPEG images, and encodes baseline JPEG images.

## Model

`decode_image` reads the segments of a JPEG file: the quantization tables, the Huffman tables, the frame and each scan. A scan adds its part of the coefficients of each block: all of them in a baseline file, or one band and some bits of them in a progressive file.

After the last scan, the decoder multiplies the coefficients by the quantization tables. The inverse discrete cosine transform then gives the samples of each block of 8 by 8, and the decoder changes the YCbCr samples to RGB. A chroma component with fewer samples gives each pixel its nearest sample.

`encode_rgb8` and `encode_rgba8` do these steps in the opposite order, for a baseline file. The section "Encoding" tells how.

## Contracts

The decoder accepts 8-bit Huffman-coded files with one or three components: baseline (SOF0), extended (SOF1) and progressive (SOF2), with any sampling factors and with restart intervals. It refuses lossless and arithmetic-coded files with `Unsupported`. It also refuses with `Unsupported` a frame with more pixels than the limit, before it allocates for the image. `decode_image_limit` takes the limit, and `decode_image` uses `MAX_PIXELS`, the most pixels that the buffers of an image can hold. Data that ends early gives `Truncated`. A scan with corrupt data gives an image with wrong pixels in the blocks after the fault, not an error. A scan that uses a Huffman table that the file does not define is corrupt data in the same way. The pixels are RGBA, and each alpha is 255.

The encoder accepts a width and a height from 1 to 65535, and pixels with three or four bytes each. Other dimensions give `InvalidDimensions`, and pixels of a different length give `InvalidBufferLength`. A quality below 1 is 1, and a quality above 100 is 100.

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
  let red: u8[] = [255, 0, 0, 255, 255, 0, 0, 255];
  let Ok(file) = jpeg.encode_rgba8(2, 1, red, jpeg.options()) else { return 3; };
  let Ok(img) = jpeg.decode_image(file) else { return 4; };
  if (img.width != 2 || img.pixels[0] < 240u8 || img.pixels[1] > 15u8) { return 5; }
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

<<encoding options>>

<<encoding>>

<<quantization>>

<<the samples>>

<<the tokens>>

<<optimal Huffman tables>>

<<the file>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module decodes baseline and progressive JPEG images, and encodes
// baseline JPEG images.
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
  let scanned = false;
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
      if (!scanned) {
        coef = zeros(total_blocks(d) * 64);
        scanned = true;
      }
      let (next, after) = scan(d, seg, coef);
      coef = next;
      at = next_marker(d.data, after);
      continue;
    }
    match (segment(d, marker, seg, end)) {
      Ok(next) => {
        d = next;
      },
      Err(e) => {
        return Err(e);
      }
    }
    at = next_marker(d.data, end);
  }
  if (!d.frame) {
    return Err(Corrupt("no frame"));
  }
  if (!scanned) {
    return Err(Corrupt("no scan"));
  }
  return Ok(pixels(d, coef));
}

function copy(data: [u8]): u8[] {
  let b: u8[] = [];
  for x in data {
    b = b.append(x);
  }
  return b;
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

## Encoding

`encode_rgb8` and `encode_rgba8` write a baseline JPEG file with three components: Y, Cb and Cr of JFIF. The quality of `Options` scales the quantization tables of Annex K of the standard. The scale is the scale of the Independent JPEG Group code. With `S420`, each chroma sample is the mean of a block of 2 by 2 pixels. With `S444`, each pixel has its own chroma samples.

The encoder makes the file in two passes. The first pass transforms and quantizes each block, and keeps the symbols of the blocks as tokens. It also counts how often each symbol comes.

Then the encoder makes the Huffman tables for these counts, by the method of Annex K.2. These tables give the shortest data. The second pass writes the codes of the tokens. The file is smaller than a file with the tables of Annex K.3, and the pixels are the same.

### `encoding options`

```fern
<<encoding options>>=
// The chroma samples of a JPEG file: one for each pixel (4:4:4), or one
// for each block of 2 by 2 pixels (4:2:0).
pub enum Subsampling { S444, S420 }

// The quality is from 1, the smallest file, to 100, the best image.
pub struct Options { quality: i32, subsampling: Subsampling }

// Quality 75 with 4:2:0 chroma.
pub function options(): Options {
  return Options { quality: 75, subsampling: S420 };
}

pub enum EncodeError { InvalidDimensions, InvalidBufferLength }

pub function (e: EncodeError) message(): string {
  match (e) {
    InvalidDimensions => {
      return "jpeg: the width and the height must be from 1 to 65535";
    },
    InvalidBufferLength => {
      return "jpeg: the length of the pixels does not agree with the dimensions";
    }
  }
}

pub function (e: EncodeError) to_string(): string {
  return e.message();
}

impl error.Error for EncodeError {}

impl cmp.Display for EncodeError {}
```

### `encoding`

The edges of the image repeat to fill the last blocks. The encoder ignores the alpha of RGBA pixels.

```fern
<<encoding>>=
// The baseline JPEG file of `pixels`, which have red, green and blue for
// each pixel.
pub function encode_rgb8(width: i32, height: i32, pixels: [u8], opts: Options): Result[u8[], EncodeError] {
  return encode(width, height, pixels, opts, 3);
}

// The baseline JPEG file of `pixels`, which have red, green, blue and
// alpha for each pixel. The encoder ignores the alpha.
pub function encode_rgba8(width: i32, height: i32, pixels: [u8], opts: Options): Result[u8[], EncodeError] {
  return encode(width, height, pixels, opts, 4);
}

function encode(width: i32, height: i32, pixels: [u8], opts: Options, bpp: i32): Result[u8[], EncodeError] {
  if (width <= 0 || height <= 0 || width > 65535 || height > 65535) {
    return Err(InvalidDimensions);
  }
  if (pixels.len() as i64 != width as i64 * (height as i64) * (bpp as i64)) {
    return Err(InvalidBufferLength);
  }
  let quality = clamp_quality(opts.quality);
  let half = match (opts.subsampling) {
    S444 => false,
    S420 => true,
  };
  let lq = scaled_table(luma_table(), quality);
  let cq = scaled_table(chroma_table(), quality);
  let src = Source { pixels: pixels, width: width, height: height, bpp: bpp };
  let mcu = if (half) { 16 } else { 8 };
  let mcux = (width + mcu - 1) / mcu;
  let mcuy = (height + mcu - 1) / mcu;
  let y_plane = luma_plane(src, mcux * mcu, mcuy * mcu);
  let (cb_plane, cr_plane) = chroma_planes(src, mcux * 8, mcuy * 8, half);
  let (tokens, counts) = tokenize(y_plane, cb_plane, cr_plane, mcux, mcuy, half, multipliers(lq), multipliers(cq));
  let tables: Table[] = [];
  for t in 0..4 {
    tables = tables.append(optimal_table(counts, t));
  }
  let out = headers(width, height, half, lq, cq, tables);
  out = scan_bytes(out, tokens, tables);
  return Ok(out.append(255u8).append(217u8));
}

function clamp_quality(q: i32): i32 {
  if (q < 1) {
    return 1;
  }
  if (q > 100) {
    return 100;
  }
  return q;
}

// The pixels of the image, `bpp` bytes each.
struct Source { pixels: [u8], width: i32, height: i32, bpp: i32 }
```

### `quantization`

The AAN transform gives each coefficient times a scale factor. The multiplier of each coefficient removes the scale and divides by the quantizer, so the quantization is one multiplication.

```fern
<<quantization>>=
// The quantization tables of Annex K of the JPEG standard, for quality 50,
// in natural order.
function luma_table(): i32[] {
  return [16, 11, 10, 16, 24, 40, 51, 61, 12, 12, 14, 19, 26, 58, 60, 55, 14, 13, 16, 24, 40, 57, 69, 56, 14, 17, 22, 29, 51, 87, 80, 62, 18, 22, 37, 56, 68, 109, 103, 77, 24, 35, 55, 64, 81, 104, 113, 92, 49, 64, 78, 87, 103, 121, 120, 101, 72, 92, 95, 98, 112, 100, 103, 99];
}

function chroma_table(): i32[] {
  return [17, 18, 24, 47, 99, 99, 99, 99, 18, 21, 26, 66, 99, 99, 99, 99, 24, 26, 56, 99, 99, 99, 99, 99, 47, 66, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99];
}

// The table `base` for `quality`, with the scale of the Independent JPEG
// Group: 5000 / quality below 50, and 200 - 2 * quality from 50.
function scaled_table(base: [i32], quality: i32): i32[] {
  let scale = if (quality < 50) { 5000 / quality } else { 200 - quality * 2 };
  let out: i32[] = [];
  for v in base {
    let q = (v * scale + 50) / 100;
    if (q < 1) {
      q = 1;
    } else if (q > 255) {
      q = 255;
    }
    out = out.append(q);
  }
  return out;
}

// The scale factors of the AAN transform: cos(k pi / 16) * sqrt(2) for k
// from 1, and 1 for k = 0.
function aan_scale(): f64[] {
  return [1.0, 1.387039845, 1.306562965, 1.175875602, 1.0, 0.785694958, 0.5411961, 0.275899379];
}

// The factor for each coefficient of the AAN transform that divides it by
// its quantizer and removes the scale of the transform.
function multipliers(q: [i32]): f64[] {
  let aan = aan_scale();
  let out: f64[] = [];
  for r in 0..8 {
    for c in 0..8 {
      out = out.append(1.0 / (q[r * 8 + c] as f64 * aan[r] * aan[c] * 8.0));
    }
  }
  return out;
}

function at_most(v: i32, most: i32): i32 {
  if (v > most) {
    return most;
  }
  return v;
}
```

### `the samples`

The conversion uses the coefficients of JFIF in fixed point with 16 fraction bits. The Independent JPEG Group code does the same.

```fern
<<the samples>>=
// The Y samples of JFIF, at w by h. A sample past the edge of the image
// repeats the last column or row.
function luma_plane(s: Source, w: i32, h: i32): u8[] {
  let out = __alloc_u8(w * h);
  let px = s.pixels;
  for y in 0..h {
    let row = at_most(y, s.height - 1) * s.width;
    for x in 0..w {
      let at = (row + at_most(x, s.width - 1)) * s.bpp;
      let r = px[at] as i32;
      let g = px[at + 1] as i32;
      let b = px[at + 2] as i32;
      out = out.with(y * w + x, (19595 * r + 38470 * g + 7471 * b + 32768 >> 16) as u8);
    }
  }
  return out;
}

// The Cb and Cr samples of JFIF, at w by h. With `half`, each sample is the
// mean of a block of 2 by 2 pixels.
function chroma_planes(s: Source, w: i32, h: i32, half: boolean): (u8[], u8[]) {
  let cb = __alloc_u8(w * h);
  let cr = __alloc_u8(w * h);
  let px = s.pixels;
  let step = if (half) { 2 } else { 1 };
  let shift = if (half) { 18 } else { 16 };
  let round = (128 << shift) + (1 << shift - 1) - 1;
  for y in 0..h {
    for x in 0..w {
      let r = 0;
      let g = 0;
      let b = 0;
      for dy in 0..step {
        let row = at_most(y * step + dy, s.height - 1) * s.width;
        for dx in 0..step {
          let at = (row + at_most(x * step + dx, s.width - 1)) * s.bpp;
          r += px[at] as i32;
          g += px[at + 1] as i32;
          b += px[at + 2] as i32;
        }
      }
      cb = cb.with(y * w + x, (-11059 * r - 21709 * g + 32768 * b + round >> shift) as u8);
      cr = cr.with(y * w + x, (32768 * r - 27439 * g - 5329 * b + round >> shift) as u8);
    }
  }
  return (cb, cr);
}
```

### `the tokens`

A token keeps a symbol and the extra bits of its value in one i32. The DC symbol is the count of bits of the difference from the DC value of the block before. An AC symbol is the count of zero coefficients before the value, then the count of bits of the value. The symbol 240 is a run of 16 zero coefficients, and 0 ends a block.

```fern
<<the tokens>>=
// The four Huffman tables: the DC and AC tables of Y, then of Cb and Cr.
const DC_LUMA: i32 = 0;

const AC_LUMA: i32 = 1;

const DC_CHROMA: i32 = 2;

const AC_CHROMA: i32 = 3;

// The symbols of each table, 256 for the symbols and one for the code
// point that Annex K keeps free, so that no code is all ones.
const SYMBOLS: i32 = 257;

// Each block of each component, in the order of the scan, as tokens. A
// token is the table << 28, the symbol << 16 and the extra bits of the
// value. The counts give how often each symbol of each table comes.
function tokenize(y: [u8], cb: [u8], cr: [u8], mcux: i32, mcuy: i32, half: boolean, lm: [f64], cm: [f64]): (i32[], i32[]) {
  let zz = zigzag();
  let tokens: i32[] = [];
  let counts = __alloc_i32(4 * SYMBOLS);
  let buf: f64[] = [];
  for i in 0..64 {
    buf = buf.append(0.0);
  }
  let yw = if (half) { mcux * 16 } else { mcux * 8 };
  let cw = mcux * 8;
  let py = 0;
  let pb = 0;
  let pr = 0;
  for my in 0..mcuy {
    for mx in 0..mcux {
      if (half) {
        for k in 0..4 {
          buf = fdct(gather(buf, y, yw, mx * 16 + k % 2 * 8, my * 16 + k / 2 * 8));
          let (t, c, p) = encode_block(tokens, counts, buf, lm, zz, py, DC_LUMA);
          tokens = t;
          counts = c;
          py = p;
        }
      } else {
        buf = fdct(gather(buf, y, yw, mx * 8, my * 8));
        let (t, c, p) = encode_block(tokens, counts, buf, lm, zz, py, DC_LUMA);
        tokens = t;
        counts = c;
        py = p;
      }
      buf = fdct(gather(buf, cb, cw, mx * 8, my * 8));
      let (t1, c1, p1) = encode_block(tokens, counts, buf, cm, zz, pb, DC_CHROMA);
      buf = fdct(gather(buf, cr, cw, mx * 8, my * 8));
      let (t2, c2, p2) = encode_block(t1, c1, buf, cm, zz, pr, DC_CHROMA);
      tokens = t2;
      counts = c2;
      pb = p1;
      pr = p2;
    }
  }
  return (tokens, counts);
}

// The 64 samples of the block at x0 and y0 of `plane`, less 128.
function gather(own buf: f64[], plane: [u8], stride: i32, x0: i32, y0: i32): f64[] {
  for y in 0..8 {
    let row = (y0 + y) * stride + x0;
    for x in 0..8 {
      buf = buf.with(y * 8 + x, (plane[row + x] as i32 - 128) as f64);
    }
  }
  return buf;
}

// The AAN forward transform of the block: each row, then each column.
function fdct(own b: f64[]): f64[] {
  for r in 0..8 {
    b = fdct8(b, r * 8, 1);
  }
  for c in 0..8 {
    b = fdct8(b, c, 8);
  }
  return b;
}

// The transform of the eight values from `at`, `step` apart, in place.
function fdct8(own b: f64[], at: i32, step: i32): f64[] {
  let d0 = b[at];
  let d1 = b[at + step];
  let d2 = b[at + 2 * step];
  let d3 = b[at + 3 * step];
  let d4 = b[at + 4 * step];
  let d5 = b[at + 5 * step];
  let d6 = b[at + 6 * step];
  let d7 = b[at + 7 * step];
  let t0 = d0 + d7;
  let t7 = d0 - d7;
  let t1 = d1 + d6;
  let t6 = d1 - d6;
  let t2 = d2 + d5;
  let t5 = d2 - d5;
  let t3 = d3 + d4;
  let t4 = d3 - d4;
  let e10 = t0 + t3;
  let e13 = t0 - t3;
  let e11 = t1 + t2;
  let e12 = t1 - t2;
  b = b.with(at, e10 + e11);
  b = b.with(at + 4 * step, e10 - e11);
  let z1 = (e12 + e13) * 0.707106781;
  b = b.with(at + 2 * step, e13 + z1);
  b = b.with(at + 6 * step, e13 - z1);
  let o10 = t4 + t5;
  let o11 = t5 + t6;
  let o12 = t6 + t7;
  let z5 = (o10 - o12) * 0.382683433;
  let z2 = 0.5411961 * o10 + z5;
  let z4 = 1.306562965 * o12 + z5;
  let z3 = o11 * 0.707106781;
  let z11 = t7 + z3;
  let z13 = t7 - z3;
  b = b.with(at + 5 * step, z13 + z2);
  b = b.with(at + 3 * step, z13 - z2);
  b = b.with(at + step, z11 + z4);
  return b.with(at + 7 * step, z11 - z4);
}

// The count of bits of the magnitude of `v`.
function magnitude(v: i32): i32 {
  let a = if (v < 0) { -v } else { v };
  let n = 0;
  while (a > 0) {
    a >>= 1;
    n += 1;
  }
  return n;
}

// The token of `symbol` in `table` with the value `v` of `size` bits.
function token(table: i32, symbol: i32, v: i32, size: i32): i32 {
  let bits = if (v < 0) { v + (1 << size) - 1 } else { v };
  return table << 28 | symbol << 16 | bits & 65535;
}

// Quantizes the transformed block `b` and adds its tokens. `pred` is the
// DC value of the block before it in the component. It gives the tokens,
// the counts and the DC value of this block.
function encode_block(own tokens: i32[], own counts: i32[], b: [f64], mul: [f64], zz: [i32], pred: i32, dc_table: i32): (i32[], i32[], i32) {
  let ac_table = dc_table + 1;
  let dc = quantize(b[0] * mul[0]);
  let diff = dc - pred;
  let size = magnitude(diff);
  tokens = tokens.append(token(dc_table, size, diff, size));
  counts = counts.with(dc_table * SYMBOLS + size, counts[dc_table * SYMBOLS + size] + 1);
  let run = 0;
  for i in 1..64 {
    let k = zz[i];
    let v = quantize(b[k] * mul[k]);
    if (v == 0) {
      run += 1;
    } else {
      while (run > 15) {
        tokens = tokens.append(token(ac_table, 240, 0, 0));
        counts = counts.with(ac_table * SYMBOLS + 240, counts[ac_table * SYMBOLS + 240] + 1);
        run -= 16;
      }
      let s = magnitude(v);
      let symbol = run << 4 | s;
      tokens = tokens.append(token(ac_table, symbol, v, s));
      counts = counts.with(ac_table * SYMBOLS + symbol, counts[ac_table * SYMBOLS + symbol] + 1);
      run = 0;
    }
  }
  if (run > 0) {
    tokens = tokens.append(token(ac_table, 0, 0, 0));
    counts = counts.with(ac_table * SYMBOLS, counts[ac_table * SYMBOLS] + 1);
  }
  return (tokens, counts, dc);
}

// `v` rounded to the nearest integer, with a half away from zero rounded
// up, as in the Independent JPEG Group code.
function quantize(v: f64): i32 {
  return (v + 16384.5).floor() as i32 - 16384;
}
```

### `optimal Huffman tables`

Annex K.2 joins the two least frequent symbols until one tree is left. The joined codes can be longer than the 16 bits that a JPEG table holds. Then the method moves codes to shorter lengths: two codes of the longest length and one shorter code at a time. The extra code point gets the longest code, so that no symbol has a code of only one bits.

```fern
<<optimal Huffman tables>>=
// A Huffman table: the count of codes of each length from 1 to 16, the
// symbols in the order of their codes, and the code and the length of
// each symbol.
struct Table { bits: i32[], values: i32[], codes: i32[], sizes: i32[] }

// The Huffman table for the counts of table `t`, with no code longer than
// 16 bits, by the method of Annex K.2.
function optimal_table(counts: [i32], t: i32): Table {
  let freq: i32[] = [];
  for i in 0..SYMBOLS - 1 {
    freq = freq.append(counts[t * SYMBOLS + i]);
  }
  freq = freq.append(1);
  let size: i32[] = [];
  let others: i32[] = [];
  for i in 0..SYMBOLS {
    size = size.append(0);
    others = others.append(-1);
  }
  while (true) {
    let c1 = least(freq, -1);
    let c2 = least(freq, c1);
    if (c2 < 0) {
      break;
    }
    freq = freq.with(c1, freq[c1] + freq[c2]).with(c2, 0);
    size = size.with(c1, size[c1] + 1);
    while (others[c1] >= 0) {
      c1 = others[c1];
      size = size.with(c1, size[c1] + 1);
    }
    others = others.with(c1, c2);
    size = size.with(c2, size[c2] + 1);
    while (others[c2] >= 0) {
      c2 = others[c2];
      size = size.with(c2, size[c2] + 1);
    }
  }
  let bits: i32[] = [];
  for i in 0..33 {
    bits = bits.append(0);
  }
  for i in 0..SYMBOLS {
    if (size[i] > 0) {
      bits = bits.with(size[i], bits[size[i]] + 1);
    }
  }
  let i = 32;
  while (i > 16) {
    while (bits[i] > 0) {
      let j = i - 2;
      while (bits[j] == 0) {
        j -= 1;
      }
      bits = bits.with(i, bits[i] - 2);
      bits = bits.with(i - 1, bits[i - 1] + 1);
      bits = bits.with(j + 1, bits[j + 1] + 2);
      bits = bits.with(j, bits[j] - 1);
    }
    i -= 1;
  }
  // The longest code goes to the free code point, which no symbol uses.
  let longest = 16;
  while (bits[longest] == 0) {
    longest -= 1;
  }
  bits = bits.with(longest, bits[longest] - 1);
  let values: i32[] = [];
  for len in 1..33 {
    for v in 0..SYMBOLS - 1 {
      if (size[v] == len) {
        values = values.append(v);
      }
    }
  }
  return codes_of(bits, values);
}

// The index of the symbol with the smallest count that is not 0, other
// than `not`, with the last one of equal counts; or -1 when there is none.
function least(freq: [i32], not: i32): i32 {
  let best = -1;
  let v = 2147483647;
  for i in 0..freq.len() {
    if (freq[i] > 0 && freq[i] <= v && i != not) {
      v = freq[i];
      best = i;
    }
  }
  return best;
}

// The canonical codes of Annex C for the lengths `bits` and the symbols
// `values`.
function codes_of(bits: i32[], values: i32[]): Table {
  let codes = __alloc_i32(256);
  let sizes = __alloc_i32(256);
  let code = 0;
  let k = 0;
  for len in 1..17 {
    for i in 0..bits[len] {
      codes = codes.with(values[k], code);
      sizes = sizes.with(values[k], len);
      code += 1;
      k += 1;
    }
    code <<= 1;
  }
  return Table { bits: bits.truncate(17), values: values, codes: codes, sizes: sizes };
}
```

### `the file`

```fern
<<the file>>=
function put_u16(own out: u8[], v: i32): u8[] {
  return out.append((v >> 8) as u8).append((v & 255) as u8);
}

function marker(own out: u8[], m: i32, length: i32): u8[] {
  return put_u16(out.append(255u8).append(m as u8), length);
}

// The markers before the data of the scan: the start of the image, the
// JFIF header, the quantization tables, the frame, the Huffman tables and
// the header of the scan.
function headers(width: i32, height: i32, half: boolean, lq: [i32], cq: [i32], tables: [Table]): u8[] {
  let zz = zigzag();
  let out: u8[] = [255u8, 216u8];
  out = marker(out, 224, 16);
  for c in [74, 70, 73, 70, 0, 1, 1, 0, 0, 1, 0, 1, 0, 0] {
    out = out.append(c as u8);
  }
  out = marker(out, 219, 132);
  out = out.append(0u8);
  for k in 0..64 {
    out = out.append(lq[zz[k]] as u8);
  }
  out = out.append(1u8);
  for k in 0..64 {
    out = out.append(cq[zz[k]] as u8);
  }
  out = marker(out, 192, 17).append(8u8);
  out = put_u16(put_u16(out, height), width).append(3u8);
  let luma = if (half) { 34u8 } else { 17u8 };
  out = out.append(1u8).append(luma).append(0u8);
  out = out.append(2u8).append(17u8).append(1u8);
  out = out.append(3u8).append(17u8).append(1u8);
  let length = 2;
  for t in tables {
    length += 17 + t.values.len();
  }
  out = marker(out, 196, length);
  for t in 0..4 {
    out = out.append((t % 2 << 4 | t / 2) as u8);
    for len in 1..17 {
      out = out.append(tables[t].bits[len] as u8);
    }
    for v in tables[t].values {
      out = out.append(v as u8);
    }
  }
  out = marker(out, 218, 12).append(3u8);
  out = out.append(1u8).append(0u8).append(2u8).append(17u8).append(3u8).append(17u8);
  return out.append(0u8).append(63u8).append(0u8);
}

// The entropy-coded data of the tokens: each code, then its extra bits.
// A byte 255 takes a 0 after it, and the last byte ends with one bits.
function scan_bytes(own out: u8[], tokens: [i32], tables: [Table]): u8[] {
  let codes: i32[] = [];
  let sizes: i32[] = [];
  for t in tables {
    for i in 0..256 {
      codes = codes.append(t.codes[i]);
      sizes = sizes.append(t.sizes[i]);
    }
  }
  let acc = 0i64;
  let n = 0;
  for tk in tokens {
    let table = tk >> 28;
    let symbol = tk >> 16 & 255;
    let at = table * 256 + symbol;
    let extra = if (table % 2 == 0) { symbol } else { symbol & 15 };
    acc = acc << sizes[at] as i64 | codes[at] as i64;
    n += sizes[at];
    if (extra > 0) {
      acc = acc << extra as i64 | (tk & (1 << extra) - 1) as i64;
      n += extra;
    }
    while (n >= 8) {
      n -= 8;
      let byte = (acc >> n as i64 & 255i64) as i32;
      out = out.append(byte as u8);
      if (byte == 255) {
        out = out.append(0u8);
      }
    }
    acc &= (1i64 << n as i64) - 1i64;
  }
  if (n > 0) {
    let byte = (acc as i32 << 8 - n | (1 << 8 - n) - 1) & 255;
    out = out.append(byte as u8);
    if (byte == 255) {
      out = out.append(0u8);
    }
  }
  return out;
}
```
