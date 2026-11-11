# Audio file tags

This module reads the title, the artist, the album and the track number that an audio file holds about itself.

## Model

`read` looks at the start of a file and reads the tags of its format:

- An MP3 file has an ID3v2 tag at its start, in the versions 2.2, 2.3 and 2.4. A text is in Latin-1, in UTF-16 or in UTF-8. When the file has no ID3v2 tag, `read` takes the ID3v1 tag of its last 128 bytes.
- A FLAC file has a Vorbis comment block: a list of `NAME=value` texts in UTF-8.
- A WAV file has a `LIST` chunk of the type `INFO`, or an `id3 ` chunk that holds an ID3v2 tag.

A field that the file does not give is "" or 0. The module reads texts only. It reads no picture, and it does not decode the sound.

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

<<files>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the tags of an MP3, a FLAC or a WAV file.
import "std/audio/wire";
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
  let t = id3v2(input);
  if (!t.empty()) {
    return t;
  }
  return id3v1(input);
}
```
