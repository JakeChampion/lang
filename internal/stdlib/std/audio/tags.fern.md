# Audio file tags

This module reads the title, the artist, the album and the track number that an audio file holds about itself.

## Model

`read` looks at the start of a file and reads the tags of its format:

- An MP3 file has an ID3v2 tag at its start, in the versions 2.2, 2.3 and 2.4. A text is in Latin-1, in UTF-16 or in UTF-8. When the file has no ID3v2 tag, `read` takes the ID3v1 tag of its last 128 bytes.
- A FLAC file has a Vorbis comment block: a list of `NAME=value` texts in UTF-8.
- A WAV file has a `LIST` chunk of the type `INFO`, or an `id3 ` chunk that holds an ID3v2 tag.
- An MP4 or M4A file has an `ilst` box in its `moov` box. `mp4` reads it from the whole file, or from the `moov` box only.
- An Ogg file with Vorbis or Opus sound has its Vorbis comments in its second packet.

A field that the file does not give is "" or 0. `picture` gives the bytes of the image that the file holds for its album, as a JPEG or a PNG image. The module does not decode that image or the sound.

## Contracts

The input is the whole file, or its start as far as the end of its tags. A tag that goes past the end of the input gives the fields before that point. No input makes the functions fail: bytes that are not a tag give empty tags.

A track number such as `3/12` gives 3. A date such as `2019-05-01` gives the year 2019.

## Executable example

```fern test name=std-audio-tags-example
import "std/audio/tags";

function main(): i32 {
  // An ID3v1 tag of 128 bytes: TAG, then the title, the artist and the
  // album in 30 bytes each, the year in 4, a comment in 28, a zero, the
  // track number, and the genre.
  let file: u8[] = [];
  for part in ["TAG", "Morning", "Fern", "First Light", "2026"] {
    let width = 30;
    if (part == "TAG") {
      width = 3;
    }
    if (part == "2026") {
      width = 4;
    }
    for i in 0..width {
      if (i < part.len()) {
        file = file.append(part[i]);
      } else {
        file = file.append(0u8);
      }
    }
  }
  for i in 0..31 {
    file = file.append(0u8);
  }
  let t = tags.read(file.with(126, 7u8));
  if (t.title == "Morning" && t.artist == "Fern" && t.album == "First Light" && t.year == 2026 && t.track == 7) {
    return 0;
  }
  return 1;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<texts>>

<<ID3>>

<<Vorbis comments>>

<<RIFF>>

<<MP4>>

<<Ogg>>

<<pictures>>

<<files>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the tags of an MP3, a FLAC or a WAV file.
import "std/audio/wire";
import "std/base64";
import "std/result";
import "std/i32";
import "std/option";
import "std/unicode";
import "std/utf8";

// What a file tells about its sound. A field that the file does not give
// is "" or 0.
pub struct Tags { title: string, artist: string, album: string, album_artist: string, track: i32, year: i32 }

pub function none(): Tags {
  return Tags { title: "", artist: "", album: "", album_artist: "", track: 0, year: 0 };
}

// The fields of the tags, for the tables of the names of each format.
const TITLE: i32 = 0;

const ARTIST: i32 = 1;

const ALBUM: i32 = 2;

const ALBUM_ARTIST: i32 = 3;

const TRACK: i32 = 4;

const YEAR: i32 = 5;

// The tags with the text `text` in the field `field`. An empty text and a
// field that is not one of the six change nothing.
function with_field(t: Tags, field: i32, text: string): Tags {
  if (text.len() == 0) {
    return t;
  }
  if (field == TITLE) {
    return Tags { ...t, title: text };
  }
  if (field == ARTIST) {
    return Tags { ...t, artist: text };
  }
  if (field == ALBUM) {
    return Tags { ...t, album: text };
  }
  if (field == ALBUM_ARTIST) {
    return Tags { ...t, album_artist: text };
  }
  if (field == TRACK) {
    return Tags { ...t, track: number(text) };
  }
  if (field == YEAR) {
    return Tags { ...t, year: number(text) };
  }
  return t;
}

// The field of the name `name` in the table `names`, or -1.
function field_of(names: (string, i32)[], name: string): i32 {
  for pair in names {
    let (known, field) = pair;
    if (known == name) {
      return field;
    }
  }
  return -1;
}

// The tags have no field.
pub function (t: Tags) empty(): boolean {
  return t.title.len() == 0 && t.artist.len() == 0 && t.album.len() == 0 && t.album_artist.len() == 0 && t.track == 0 && t.year == 0;
}
```

### `texts`

```fern
<<texts>>=
// The first number in `s`, or 0: 3 for "3/12", and 2019 for "2019-05-01".
function number(s: string): i32 {
  let n = 0;
  let seen = false;
  for i in 0..s.len() {
    let d = s[i] as i32 - 48;
    if (d >= 0 && d <= 9 && n < 100000000) {
      n = n * 10 + d;
      seen = true;
    } else if (seen) {
      return n;
    }
  }
  return n;
}

function text_of(cps: i32[]): string {
  let out = "";
  for cp in cps {
    if let Some(ch) = utf8.char_from_i32(cp) {
      out += utf8.encode(ch);
    }
  }
  return out.trim().to_owned();
}

// The Latin-1 text of the bytes from `from` to `to`, as far as its first
// zero byte.
function latin1(input: [u8], from: i32, to: i32): string {
  let cps: i32[] = [];
  for i in from..to.min(input.len()) {
    if (input[i] == 0u8) {
      return text_of(cps);
    }
    cps = cps.append(input[i] as i32);
  }
  return text_of(cps);
}

// The UTF-8 text of the bytes from `from` to `to`, as far as its first
// zero byte.
function utf8_text(input: [u8], from: i32, to: i32): string {
  let end = to.min(input.len());
  let stop = end;
  for i in from..end {
    if (stop == end && input[i] == 0u8) {
      stop = i;
    }
  }
  let bytes: u8[] = [];
  for i in from..stop {
    bytes = bytes.append(input[i]);
  }
  return utf8.from_bytes_lossy(bytes).trim().to_owned();
}

// The byte order of a UTF-16 text at `at`, and the byte that its units
// start at: a byte order mark tells the order, and `big` is the order of a
// text with no mark.
function byte_order(input: [u8], at: i32, end: i32, big: boolean): (boolean, i32) {
  if (at + 2 <= end && input[at] == 255u8 && input[at + 1] == 254u8) {
    return (false, at + 2);
  }
  if (at + 2 <= end && input[at] == 254u8 && input[at + 1] == 255u8) {
    return (true, at + 2);
  }
  return (big, at);
}

// The code points with the UTF-16 unit `unit` after the unit `high`: two
// units of a pair give one code point.
function with_unit(own cps: i32[], high: i32, unit: i32): i32[] {
  if (unit >= 56320 && unit < 57344 && high != 0) {
    return cps.append(65536 + (high - 55296 << 10) + unit - 56320);
  }
  return cps.append(unit);
}

// The UTF-16 text of the bytes from `from` to `to`, as far as its first
// zero unit. `big` is the byte order when the text has no byte order mark.
function utf16(input: [u8], from: i32, to: i32, big: boolean): string {
  let end = to.min(input.len());
  let (order, start) = byte_order(input, from, end, big);
  let at = start;
  let cps: i32[] = [];
  let high = 0;
  while (at + 2 <= end) {
    let unit = input[at] as i32 | input[at + 1] as i32 << 8;
    if (order) {
      unit = input[at] as i32 << 8 | input[at + 1] as i32;
    }
    at += 2;
    if (unit == 0) {
      return text_of(cps);
    }
    if (unit >= 55296 && unit < 56320) {
      high = unit;
    } else {
      cps = with_unit(cps, high, unit);
      high = 0;
    }
  }
  return text_of(cps);
}
```

### `ID3`

A size of ID3v2 has 7 bits in each byte, so no byte of it looks like the start of an MP3 frame. The frames of the version 2.3 have a plain size.

```fern
<<ID3>>=
function syncsafe(input: [u8], at: i32): i32 {
  return (input[at] as i32 & 127) << 21 | (input[at + 1] as i32 & 127) << 14 | (input[at + 2] as i32 & 127) << 7 | input[at + 3] as i32 & 127;
}

// The text of an ID3v2 text frame from `from` to `to`: its first byte tells
// how the text is written.
function frame_text(input: [u8], from: i32, to: i32): string {
  if (from >= to || to > input.len()) {
    return "";
  }
  let kind = input[from];
  if (kind == 1u8) {
    return utf16(input, from + 1, to, false);
  }
  if (kind == 2u8) {
    return utf16(input, from + 1, to, true);
  }
  if (kind == 3u8) {
    return utf8_text(input, from + 1, to);
  }
  return latin1(input, from + 1, to);
}

// The names of the text frames of ID3v2: four letters in the versions 2.3
// and 2.4, and three in the version 2.2.
function id3_names(): (string, i32)[] {
  return [("TIT2", TITLE), ("TT2", TITLE), ("TPE1", ARTIST), ("TP1", ARTIST), ("TALB", ALBUM), ("TAL", ALBUM), ("TPE2", ALBUM_ARTIST), ("TP2", ALBUM_ARTIST), ("TRCK", TRACK), ("TRK", TRACK), ("TDRC", YEAR), ("TYER", YEAR), ("TYE", YEAR)];
}

// The name of a frame is capital letters and digits.
function frame_name(input: [u8], at: i32, n: i32): string {
  let cps: i32[] = [];
  for i in at..at + n {
    let c = input[i] as i32;
    if (!(c >= 65 && c <= 90 || c >= 48 && c <= 57)) {
      return "";
    }
    cps = cps.append(c);
  }
  return text_of(cps);
}

// The byte that the frames of an ID3v2 tag start at: after its header of
// 10 bytes at `start`, and after its extended header. The size of an
// extended header has the size field itself in the version 2.4, and not in
// the version 2.3.
function frames_start(input: [u8], start: i32, end: i32): i32 {
  let version = input[start + 3] as i32;
  let at = start + 10;
  if (version < 3 || (input[start + 5] as i32 & 64) == 0 || at + 4 > end) {
    return at;
  }
  if (version == 4) {
    return at + syncsafe(input, at);
  }
  return at + 4 + wire.be(input, at, 4) as i32;
}

// The tags of the frames of the version `version` from `from` to `end`.
function frames(input: [u8], from: i32, end: i32, version: i32): Tags {
  let name = 4;
  let head = 10;
  if (version == 2) {
    name = 3;
    head = 6;
  }
  let names = id3_names();
  let t = none();
  let at = from;
  while (at + head <= end) {
    let id = frame_name(input, at, name);
    let size = wire.be(input, at + name, name) as i32;
    if (version == 4) {
      size = syncsafe(input, at + 4);
    }
    if (id.len() == 0 || size <= 0 || at + head + size > end) {
      return t;
    }
    t = with_field(t, field_of(names, id), frame_text(input, at + head, at + head + size));
    at += head + size;
  }
  return t;
}

// The tags of the ID3v2 tag that starts at the byte `start`, or empty
// tags.
pub function id3v2_at(input: [u8], start: i32): Tags {
  if (start < 0 || start + 10 > input.len() || !wire.tag(input, start, "ID3")) {
    return none();
  }
  let end = (start + 10 + syncsafe(input, start + 6)).min(input.len());
  return frames(input, frames_start(input, start, end), end, input[start + 3] as i32);
}

// The tags of the ID3v2 tag at the start of the input.
pub function id3v2(input: [u8]): Tags {
  return id3v2_at(input, 0);
}

// The tags of the ID3v1 tag in the last 128 bytes of the input.
pub function id3v1(input: [u8]): Tags {
  let at = input.len() - 128;
  if (at < 0 || !wire.tag(input, at, "TAG")) {
    return none();
  }
  let track = 0;
  if (input[at + 125] == 0u8) {
    track = input[at + 126] as i32;
  }
  return Tags { title: latin1(input, at + 3, at + 33), artist: latin1(input, at + 33, at + 63), album: latin1(input, at + 63, at + 93), album_artist: "", track: track, year: number(latin1(input, at + 93, at + 97)) };
}
```

### `Vorbis comments`

```fern
<<Vorbis comments>>=
function vorbis_names(): (string, i32)[] {
  return [("TITLE", TITLE), ("ARTIST", ARTIST), ("ALBUM", ALBUM), ("ALBUMARTIST", ALBUM_ARTIST), ("ALBUM ARTIST", ALBUM_ARTIST), ("TRACKNUMBER", TRACK), ("DATE", YEAR), ("YEAR", YEAR)];
}

// The tags with the Vorbis comment `NAME=value`. The name has small or
// capital letters.
function with_comment(t: Tags, comment: string): Tags {
  let eq = -1;
  for i in 0..comment.len() {
    if (eq < 0 && comment[i] == b'=') {
      eq = i;
    }
  }
  if (eq <= 0) {
    return t;
  }
  return with_field(t, field_of(vorbis_names(), comment.take(eq).to_upper()), comment.drop(eq + 1).trim().to_owned());
}

// The tags of the Vorbis comments from `from` to `to`: the name of the
// encoder, then the number of the comments, then each comment after its
// length.
function comments(input: [u8], from: i32, to: i32): Tags {
  let t = none();
  if (from + 8 > to) {
    return t;
  }
  let at = from + 4 + wire.le(input, from, 4) as i32;
  if (at < from || at + 4 > to) {
    return t;
  }
  let count = wire.le(input, at, 4) as i32;
  at += 4;
  let seen = 0;
  while (seen < count && at + 4 <= to) {
    let n = wire.le(input, at, 4) as i32;
    at += 4;
    if (n < 0 || at + n > to) {
      return t;
    }
    t = with_comment(t, utf8_text(input, at, at + n));
    at += n;
    seen += 1;
  }
  return t;
}

// The tags of the Vorbis comment block of a FLAC file.
pub function flac(input: [u8]): Tags {
  if (!wire.tag(input, 0, "fLaC")) {
    return none();
  }
  let at = 4;
  while (at + 4 <= input.len()) {
    let head = input[at] as i32;
    let size = wire.be(input, at + 1, 3) as i32;
    if ((head & 127) == 4) {
      return comments(input, at + 4, (at + 4 + size).min(input.len()));
    }
    if ((head & 128) != 0) {
      return none();
    }
    at += 4 + size;
  }
  return none();
}
```

### `RIFF`

```fern
<<RIFF>>=
function info_names(): (string, i32)[] {
  return [("INAM", TITLE), ("IART", ARTIST), ("IPRD", ALBUM), ("ITRK", TRACK), ("IPRT", TRACK), ("ICRD", YEAR)];
}

function info(input: [u8], from: i32, to: i32): Tags {
  let t = none();
  let at = from;
  while (at + 8 <= to) {
    let size = wire.le(input, at + 4, 4) as i32;
    if (size < 0 || at + 8 + size > to) {
      return t;
    }
    t = with_field(t, field_of(info_names(), frame_name(input, at, 4)), utf8_text(input, at + 8, at + 8 + size));
    at += 8 + size + size % 2;
  }
  return t;
}

// The tags of a WAV file: its LIST chunk of the type INFO, or the ID3v2
// tag of its `id3 ` chunk.
pub function wav(input: [u8]): Tags {
  if (!wire.tag(input, 0, "RIFF") || input.len() < 12) {
    return none();
  }
  let at = 12;
  while (at + 8 <= input.len()) {
    let size = wire.le(input, at + 4, 4) as i32;
    if (size < 0) {
      return none();
    }
    let end = (at + 8 + size).min(input.len());
    if (wire.tag(input, at, "LIST") && at + 12 <= end && wire.tag(input, at + 8, "INFO")) {
      return info(input, at + 12, end);
    }
    if (wire.tag(input, at, "id3 ") || wire.tag(input, at, "ID3 ")) {
      return id3v2_at(input, at + 8);
    }
    at += 8 + size + size % 2;
  }
  return none();
}
```

### `MP4`

The items of the `ilst` box have a name of four bytes and a `data` box. The value of the `data` box starts after 8 bytes of type and locale. The item `trkn` has the track number in the bytes 2 and 3 of its value.

```fern
<<MP4>>=
// The content of the first box of the type `kind` in the boxes from `from`
// to `to`: its start and its end, or -1 and -1. A box that goes past `to`
// ends at `to`.
function box_in(input: [u8], from: i32, to: i32, kind: string): (i32, i32) {
  let at = from;
  while (at >= from && at + 8 <= to) {
    let size = wire.be(input, at, 4) as i32;
    if (size < 8) {
      return (-1, -1);
    }
    if (wire.tag(input, at + 4, kind)) {
      return (at + 8, (at + size).min(to));
    }
    at += size;
  }
  return (-1, -1);
}

// The field of the item of `ilst` at `at`, or -1.
function mp4_field(input: [u8], at: i32): i32 {
  if (wire.tag(input, at, "aART")) {
    return ALBUM_ARTIST;
  }
  if (wire.tag(input, at, "trkn")) {
    return TRACK;
  }
  if (input[at] != 169u8) {
    return -1;
  }
  for pair in [("nam", TITLE), ("ART", ARTIST), ("alb", ALBUM), ("day", YEAR)] {
    let (name, field) = pair;
    if (wire.tag(input, at + 1, name)) {
      return field;
    }
  }
  return -1;
}

// The content of the `ilst` box of an MP4 file, or -1 and -1.
function ilst(input: [u8]): (i32, i32) {
  let (moov, moov_end) = box_in(input, 0, input.len(), "moov");
  let (udta, udta_end) = box_in(input, moov, moov_end, "udta");
  let (meta, meta_end) = box_in(input, udta, udta_end, "meta");
  if (moov < 0 || udta < 0 || meta < 0) {
    return (-1, -1);
  }
  return box_in(input, meta + 4, meta_end, "ilst");
}

// The tags of the `ilst` box of an MP4 file. The input is the file, or its
// `moov` box.
pub function mp4(input: [u8]): Tags {
  let t = none();
  let (at, end) = ilst(input);
  while (at >= 0 && at + 8 <= end) {
    let size = wire.be(input, at, 4) as i32;
    if (size < 8) {
      return t;
    }
    let field = mp4_field(input, at + 4);
    let (value, value_end) = box_in(input, at + 8, (at + size).min(end), "data");
    if (field == TRACK && value >= 0 && value + 12 <= value_end) {
      t = Tags { ...t, track: wire.be(input, value + 10, 2) as i32 };
    } else if (field >= 0 && value >= 0 && value + 8 <= value_end) {
      t = with_field(t, field, utf8_text(input, value + 8, value_end));
    }
    at += size;
  }
  return t;
}
```

### `Ogg`

An Ogg file is a list of pages. A page has a table of segments, and a segment of less than 255 bytes ends a packet. The second packet of a Vorbis stream starts with the type 3 and `vorbis`. That of an Opus stream starts with `OpusTags`.

```fern
<<Ogg>>=
// The bytes of the second packet of the Ogg stream `input`, or the part of
// it that the input has.
function second_packet(input: [u8]): u8[] {
  let out: u8[] = [];
  let packet = 0;
  let at = 0;
  while (at + 27 <= input.len() && wire.tag(input, at, "OggS")) {
    let segments = input[at + 26] as i32;
    let data = at + 27 + segments;
    for i in 0..segments {
      if (at + 27 + i >= input.len()) {
        return out;
      }
      let n = input[at + 27 + i] as i32;
      if (packet == 1) {
        for k in data..(data + n).min(input.len()) {
          out = out.append(input[k]);
        }
      }
      data += n;
      if (n < 255) {
        packet += 1;
      }
      if (packet > 1) {
        return out;
      }
    }
    at = data;
  }
  return out;
}

// The tags of the Vorbis comments of an Ogg file with Vorbis or Opus
// sound.
pub function ogg(input: [u8]): Tags {
  let packet = second_packet(input);
  if (packet.len() > 8 && wire.tag(packet, 0, "OpusTags")) {
    return comments(packet, 8, packet.len());
  }
  if (packet.len() > 7 && packet[0] == 3u8 && wire.tag(packet, 1, "vorbis")) {
    return comments(packet, 7, packet.len());
  }
  return none();
}
```

### `pictures`

A file can hold the picture of its album as a JPEG or a PNG image. An ID3v2 tag has it in an `APIC` frame, or a `PIC` frame in the version 2.2. A FLAC file has a picture block, and an Ogg file has the same block in base64 in the comment `METADATA_BLOCK_PICTURE`. An MP4 file has the item `covr`.

```fern
<<pictures>>=
function range(input: [u8], from: i32, to: i32): u8[] {
  let out: u8[] = [];
  for i in from.max(0)..to.min(input.len()) {
    out = out.append(input[i]);
  }
  return out;
}

// The byte after the end of the text that starts at `at`: a zero byte, or
// two zero bytes for a text in UTF-16.
function after_text(input: [u8], at: i32, to: i32, wide: boolean): i32 {
  let p = at;
  while (p < to) {
    if (!wide && input[p] == 0u8) {
      return p + 1;
    }
    if (wide && p + 1 < to && input[p] == 0u8 && input[p + 1] == 0u8) {
      return p + 2;
    }
    p += if (wide) { 2 } else { 1 };
  }
  return to;
}

// The image of the picture frame from `from` to `to` of an ID3v2 tag. A
// frame of the version 2.2 has a format of three letters where the others
// have a MIME type.
function frame_picture(input: [u8], from: i32, to: i32, version: i32): u8[] {
  if (from + 6 > to) {
    return [];
  }
  let wide = input[from] == 1u8 || input[from] == 2u8;
  let p = from + 4;
  if (version > 2) {
    p = after_text(input, from + 1, to, false);
  }
  return range(input, after_text(input, p + 1, to, wide), to);
}

// The image of the first picture frame of the ID3v2 tag at `start`.
function id3_picture(input: [u8], start: i32): u8[] {
  if (start < 0 || start + 10 > input.len() || !wire.tag(input, start, "ID3")) {
    return [];
  }
  let end = (start + 10 + syncsafe(input, start + 6)).min(input.len());
  let version = input[start + 3] as i32;
  let name = if (version == 2) { 3 } else { 4 };
  let head = if (version == 2) { 6 } else { 10 };
  let at = frames_start(input, start, end);
  while (at + head <= end) {
    let id = frame_name(input, at, name);
    let size = wire.be(input, at + name, name) as i32;
    if (version == 4) {
      size = syncsafe(input, at + 4);
    }
    if (id.len() == 0 || size <= 0) {
      return [];
    }
    if (id == "APIC" || id == "PIC") {
      return frame_picture(input, at + head, (at + head + size).min(end), version);
    }
    at += head + size;
  }
  return [];
}

// The image of a FLAC picture block from `from` to `to`: its type, its
// MIME type, its description, four numbers, and then the image after its
// length.
function block_picture(input: [u8], from: i32, to: i32): u8[] {
  let p = from + 4;
  for part in 0..2 {
    if (p + 4 > to) {
      return [];
    }
    p += 4 + wire.be(input, p, 4) as i32;
  }
  p += 16;
  if (p < from || p + 4 > to) {
    return [];
  }
  let n = wire.be(input, p, 4) as i32;
  return range(input, p + 4, (p + 4 + n).min(to));
}

function flac_picture(input: [u8]): u8[] {
  let at = 4;
  while (at + 4 <= input.len()) {
    let head = input[at] as i32;
    let size = wire.be(input, at + 1, 3) as i32;
    if ((head & 127) == 6) {
      return block_picture(input, at + 4, (at + 4 + size).min(input.len()));
    }
    if ((head & 128) != 0) {
      return [];
    }
    at += 4 + size;
  }
  return [];
}

// The image of the comment METADATA_BLOCK_PICTURE of the Vorbis comments
// from `from` to `to`.
function comment_picture(input: [u8], from: i32, to: i32): u8[] {
  let name = "METADATA_BLOCK_PICTURE=";
  if (from + 8 > to) {
    return [];
  }
  let at = from + 4 + wire.le(input, from, 4) as i32;
  if (at < from || at + 4 > to) {
    return [];
  }
  let count = wire.le(input, at, 4) as i32;
  at += 4;
  let seen = 0;
  while (seen < count && at + 4 <= to) {
    let n = wire.le(input, at, 4) as i32;
    at += 4;
    if (n < 0 || at + n > to) {
      return [];
    }
    if (n > name.len() && utf8_text(input, at, at + name.len()).to_upper() == name) {
      let Ok(block) = base64.decode(utf8_text(input, at + name.len(), at + n)) else {
        return [];
      };
      return block_picture(block, 0, block.len());
    }
    at += n;
    seen += 1;
  }
  return [];
}

function ogg_picture(input: [u8]): u8[] {
  let packet = second_packet(input);
  if (packet.len() > 8 && wire.tag(packet, 0, "OpusTags")) {
    return comment_picture(packet, 8, packet.len());
  }
  if (packet.len() > 7 && packet[0] == 3u8 && wire.tag(packet, 1, "vorbis")) {
    return comment_picture(packet, 7, packet.len());
  }
  return [];
}

function mp4_picture(input: [u8]): u8[] {
  let (at, end) = ilst(input);
  while (at >= 0 && at + 8 <= end) {
    let size = wire.be(input, at, 4) as i32;
    if (size < 8) {
      return [];
    }
    if (wire.tag(input, at + 4, "covr")) {
      let (value, value_end) = box_in(input, at + 8, (at + size).min(end), "data");
      if (value >= 0) {
        return range(input, value + 8, value_end);
      }
    }
    at += size;
  }
  return [];
}

// The bytes of the picture that the file `input` holds: a JPEG or a PNG
// image as the file has it, or no bytes. The input is the file as far as
// the end of its tags, or the `moov` box of an MP4 file.
pub function picture(input: [u8]): u8[] {
  if (wire.tag(input, 0, "fLaC")) {
    return flac_picture(input);
  }
  if (wire.tag(input, 0, "OggS")) {
    return ogg_picture(input);
  }
  if (input.len() >= 8 && (wire.tag(input, 4, "ftyp") || wire.tag(input, 4, "moov"))) {
    return mp4_picture(input);
  }
  return id3_picture(input, 0);
}
```

### `files`

```fern
<<files>>=
// The tags of the file `input`, by the format that its start shows.
pub function read(input: [u8]): Tags {
  if (wire.tag(input, 0, "fLaC")) {
    return flac(input);
  }
  if (wire.tag(input, 0, "RIFF")) {
    return wav(input);
  }
  if (wire.tag(input, 0, "OggS")) {
    return ogg(input);
  }
  if (input.len() >= 8 && wire.tag(input, 4, "ftyp")) {
    return mp4(input);
  }
  let t = id3v2(input);
  if (!t.empty()) {
    return t;
  }
  return id3v1(input);
}
```
