# Audio file tags

This module reads what an audio file holds about itself: the title, the artist, the album, the genre, the track number, the lyrics, and the gains of the track and of its album. It also writes the texts and the numbers of the tags of an MP3 or a FLAC file.

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

A track number such as `3/12` gives 3. A date such as `2019-05-01` gives the year 2019. A genre that is a number of the ID3v1 list, such as `(17)`, gives its name: `Rock`.

A ReplayGain text such as `-6.48 dB` gives the gain -6.48. The gains of an Opus file are for a quieter level, so they give 5 dB more. `gain` is the gain of the track, and `album_gain` is the gain of its album. An album gain keeps the steps of loudness between the tracks of one album.

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

<<writing>>

<<files>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads the tags of an MP3, a FLAC, a WAV, an MP4 or an Ogg
// file, and writes those of an MP3 or a FLAC file.
import "std/audio/wire";
import "std/base64";
import "std/result";
import "std/i32";
import "std/option";
import "std/unicode";
import "std/utf8";

// What a file tells about its sound. A field that the file does not give
// is "" or 0.
pub struct Tags { title: string, artist: string, album: string, album_artist: string, track: i32, year: i32, genre: string, lyrics: string, gain: f64, album_gain: f64 }

pub function none(): Tags {
  return Tags { title: "", artist: "", album: "", album_artist: "", track: 0, year: 0, genre: "", lyrics: "", gain: 0.0, album_gain: 0.0 };
}

// The fields of the tags, for the tables of the names of each format.
const TITLE: i32 = 0;

const ARTIST: i32 = 1;

const ALBUM: i32 = 2;

const ALBUM_ARTIST: i32 = 3;

const TRACK: i32 = 4;

const YEAR: i32 = 5;

const GENRE: i32 = 6;

const LYRICS: i32 = 8;

// The gain of the track in dB, as ReplayGain writes it.
const GAIN: i32 = 9;

// The gain of the track of an Opus file: 256 for each dB, for a level 5 dB
// below that of ReplayGain.
const OPUS_GAIN: i32 = 10;

// The gain of the album in dB, as ReplayGain writes it.
const ALBUM_GAIN: i32 = 11;

// The gain of the album of an Opus file, as for `OPUS_GAIN`.
const OPUS_ALBUM_GAIN: i32 = 12;

// The names of the gains in a frame of the person of ID3v2 and in a
// free item of MP4.
function gain_names(): (string, i32)[] {
  return [("REPLAYGAIN_TRACK_GAIN", GAIN), ("REPLAYGAIN_ALBUM_GAIN", ALBUM_GAIN)];
}

// The tags with the text `text` in a field that is a number, a genre or
// a gain.
function with_value(t: Tags, field: i32, text: string): Tags {
  if (field == TRACK) {
    return Tags { ...t, track: number(text) };
  }
  if (field == YEAR) {
    return Tags { ...t, year: number(text) };
  }
  if (field == GENRE) {
    return Tags { ...t, genre: genre_text(text) };
  }
  if (field == GAIN) {
    return Tags { ...t, gain: decibels(text) };
  }
  if (field == OPUS_GAIN) {
    return Tags { ...t, gain: decibels(text) / 256.0 + 5.0 };
  }
  if (field == ALBUM_GAIN) {
    return Tags { ...t, album_gain: decibels(text) };
  }
  if (field == OPUS_ALBUM_GAIN) {
    return Tags { ...t, album_gain: decibels(text) / 256.0 + 5.0 };
  }
  return t;
}

// The tags with the text `text` in the field `field`. An empty text and a
// field that the module does not know change nothing.
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
  if (field == LYRICS) {
    return Tags { ...t, lyrics: text };
  }
  return with_value(t, field, text);
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
  return t.title.len() == 0 && t.artist.len() == 0 && t.album.len() == 0 && t.album_artist.len() == 0 && t.track == 0 && t.year == 0 && t.genre.len() == 0 && t.lyrics.len() == 0 && t.gain == 0.0 && t.album_gain == 0.0;
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

// The digits of `s` from the byte `at` as a number, and the byte after
// them.
function digits(s: string, at: i32): (f64, i32) {
  let n = 0.0;
  let p = at;
  while (p < s.len() && s[p] >= b'0' && s[p] <= b'9') {
    n = n * 10.0 + (s[p] as i32 - 48) as f64;
    p += 1;
  }
  return (n, p);
}

// The number at the start of `text`, with a sign and a fraction: -6.48 for
// "-6.48 dB".
function decibels(text: string): f64 {
  let s = text.trim().to_owned();
  let at = 0;
  if (s.starts_with("-") || s.starts_with("+")) {
    at = 1;
  }
  let (whole, dot) = digits(s, at);
  let part = 0.0;
  if (dot < s.len() && s[dot] == b'.') {
    let (n, end) = digits(s, dot + 1);
    part = n;
    for k in dot + 1..end {
      part /= 10.0;
    }
  }
  if (s.starts_with("-")) {
    return -(whole + part);
  }
  return whole + part;
}

// The names of the genres of ID3v1, with the names that Winamp added.
function genre_names(): string[] {
  return ["Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge", "Hip-Hop", "Jazz", "Metal", "New Age", "Oldies", "Other", "Pop", "R&B", "Rap", "Reggae", "Rock", "Techno", "Industrial", "Alternative", "Ska", "Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient", "Trip-Hop", "Vocal", "Jazz+Funk", "Fusion", "Trance", "Classical", "Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise", "AlternRock", "Bass", "Soul", "Punk", "Space", "Meditative", "Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic", "Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream", "Southern Rock", "Comedy", "Cult", "Gangsta", "Top 40", "Christian Rap", "Pop/Funk", "Jungle", "Native American", "Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes", "Trailer", "Lo-Fi", "Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll", "Hard Rock", "Folk", "Folk-Rock", "National Folk", "Swing", "Fast Fusion", "Bebop", "Latin", "Revival", "Celtic", "Bluegrass", "Avantgarde", "Gothic Rock", "Progressive Rock", "Psychedelic Rock", "Symphonic Rock", "Slow Rock", "Big Band", "Chorus", "Easy Listening", "Acoustic", "Humour", "Speech", "Chanson", "Opera", "Chamber Music", "Sonata", "Symphony", "Booty Bass", "Primus", "Porn Groove", "Satire", "Slow Jam", "Club", "Tango", "Samba", "Folklore", "Ballad", "Power Ballad", "Rhythmic Soul", "Freestyle", "Duet", "Punk Rock", "Drum Solo", "A Cappella", "Euro-House", "Dance Hall", "Goa", "Drum & Bass", "Club-House", "Hardcore", "Terror", "Indie", "BritPop", "Afro-Punk", "Polsk Punk", "Beat", "Christian Gangsta Rap", "Heavy Metal", "Black Metal", "Crossover", "Contemporary Christian", "Christian Rock", "Merengue", "Salsa", "Thrash Metal", "Anime", "JPop", "Synthpop"];
}

// The name of the genre of the number `n` of ID3v1, or "".
function genre_name(n: i32): string {
  let names = genre_names();
  if (n < 0 || n >= names.len()) {
    return "";
  }
  return names[n];
}

function genre_digits_end(text: string, from: i32): i32 {
  let at = from;
  while (at < text.len() && text[at] >= 48u8 && text[at] <= 57u8) {
    at += 1;
  }
  return at;
}

// The genre of the text `text` of a tag. `(17)` and `17` give the name of
// that number, and `(17)Punk` gives the text after the number.
function genre_text(text: string): string {
  let from = 0;
  if (text.starts_with("(")) {
    from = 1;
  }
  let at = genre_digits_end(text, from);
  if (at == from) {
    return text;
  }
  if (from == 1 && (at >= text.len() || text[at] != 41u8) || from == 0 && at < text.len()) {
    return text;
  }
  if (from == 1 && at + 1 < text.len()) {
    return text.drop(at + 1).to_owned();
  }
  let name = genre_name(number(text));
  if (name.len() == 0) {
    return text;
  }
  return name;
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
  return text_as(input, input[from], from + 1, to);
}

// The text from `from` to `to` that is written as `kind` of ID3v2 tells.
function text_as(input: [u8], kind: u8, from: i32, to: i32): string {
  if (kind == 1u8) {
    return utf16(input, from, to, false);
  }
  if (kind == 2u8) {
    return utf16(input, from, to, true);
  }
  if (kind == 3u8) {
    return utf8_text(input, from, to);
  }
  return latin1(input, from, to);
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

// The tags with the gain in a frame of the person from `from` to `to`:
// such a frame has a description and then a value.
function with_user_frame(t: Tags, input: [u8], from: i32, to: i32): Tags {
  if (from + 2 > to || to > input.len()) {
    return t;
  }
  let kind = input[from];
  let value = after_text(input, from + 1, to, kind == 1u8 || kind == 2u8);
  let field = field_of(gain_names(), text_as(input, kind, from + 1, value).to_upper());
  return with_field(t, field, text_as(input, kind, value, to));
}

// The text of a lyrics frame from `from` to `to`: a language of three
// letters and a description come before it.
function frame_lyrics(input: [u8], from: i32, to: i32): string {
  if (from + 4 > to || to > input.len()) {
    return "";
  }
  let kind = input[from];
  return text_as(input, kind, after_text(input, from + 4, to, kind == 1u8 || kind == 2u8), to);
}

// The names of the text frames of ID3v2: four letters in the versions 2.3
// and 2.4, and three in the version 2.2.
function id3_names(): (string, i32)[] {
  return [("TIT2", TITLE), ("TT2", TITLE), ("TPE1", ARTIST), ("TP1", ARTIST), ("TALB", ALBUM), ("TAL", ALBUM), ("TPE2", ALBUM_ARTIST), ("TP2", ALBUM_ARTIST), ("TRCK", TRACK), ("TRK", TRACK), ("TDRC", YEAR), ("TYER", YEAR), ("TYE", YEAR), ("TCON", GENRE), ("TCO", GENRE)];
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
    t = with_frame(t, names, id, input, at + head, at + head + size);
    at += head + size;
  }
  return t;
}

// The tags with the frame `id` of an ID3v2 tag, whose content is from
// `from` to `to`: a lyrics frame, a frame of the person, or a text frame.
function with_frame(t: Tags, names: (string, i32)[], id: string, input: [u8], from: i32, to: i32): Tags {
  if (id == "USLT" || id == "ULT") {
    return with_field(t, LYRICS, frame_lyrics(input, from, to));
  }
  if (id == "TXXX" || id == "TXX") {
    return with_user_frame(t, input, from, to);
  }
  return with_field(t, field_of(names, id), frame_text(input, from, to));
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
  return Tags { title: latin1(input, at + 3, at + 33), artist: latin1(input, at + 33, at + 63), album: latin1(input, at + 63, at + 93), album_artist: "", track: track, year: number(latin1(input, at + 93, at + 97)), genre: genre_name(input[at + 127] as i32), lyrics: "", gain: 0.0, album_gain: 0.0 };
}
```

### `Vorbis comments`

```fern
<<Vorbis comments>>=
function vorbis_names(): (string, i32)[] {
  return [("TITLE", TITLE), ("ARTIST", ARTIST), ("ALBUM", ALBUM), ("ALBUMARTIST", ALBUM_ARTIST), ("ALBUM ARTIST", ALBUM_ARTIST), ("TRACKNUMBER", TRACK), ("DATE", YEAR), ("YEAR", YEAR), ("GENRE", GENRE), ("LYRICS", LYRICS), ("UNSYNCEDLYRICS", LYRICS), ("REPLAYGAIN_TRACK_GAIN", GAIN), ("REPLAYGAIN_ALBUM_GAIN", ALBUM_GAIN), ("R128_TRACK_GAIN", OPUS_GAIN), ("R128_ALBUM_GAIN", OPUS_ALBUM_GAIN)];
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
  return [("INAM", TITLE), ("IART", ARTIST), ("IPRD", ALBUM), ("ITRK", TRACK), ("IPRT", TRACK), ("ICRD", YEAR), ("IGNR", GENRE)];
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

The items of the `ilst` box have a name of four bytes and a `data` box. The value of the `data` box starts after 8 bytes of type and locale. The item `trkn` has the track number in the bytes 2 and 3 of its value. A free item `----` has a `mean` box, a `name` box and a `data` box. The name of a gain is as in a frame of the person of ID3v2.

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

// The item `gnre` of `ilst`: the number of an ID3v1 genre, plus 1.
const GENRE_NUMBER: i32 = 7;

// A free item of `ilst`, whose `name` box gives its field.
const FREE: i32 = 13;

// The field of the item of `ilst` at `at`, or -1.
function mp4_field(input: [u8], at: i32): i32 {
  if (wire.tag(input, at, "aART")) {
    return ALBUM_ARTIST;
  }
  if (wire.tag(input, at, "trkn")) {
    return TRACK;
  }
  if (wire.tag(input, at, "gnre")) {
    return GENRE_NUMBER;
  }
  if (wire.tag(input, at, "----")) {
    return FREE;
  }
  if (input[at] != 169u8) {
    return -1;
  }
  for pair in [("nam", TITLE), ("ART", ARTIST), ("alb", ALBUM), ("day", YEAR), ("gen", GENRE), ("lyr", LYRICS)] {
    let (name, field) = pair;
    if (wire.tag(input, at + 1, name)) {
      return field;
    }
  }
  return -1;
}

// The field of the free item whose boxes are from `from` to `to`, or -1.
// The text of its `name` box starts after 4 bytes of version and flags.
function free_field(input: [u8], from: i32, to: i32): i32 {
  let (name, name_end) = box_in(input, from, to, "name");
  if (name < 0 || name + 4 > name_end) {
    return -1;
  }
  return field_of(gain_names(), utf8_text(input, name + 4, name_end).to_upper());
}

// The field of the item of `ilst` from `at` to `to`, or -1.
function item_field(input: [u8], at: i32, to: i32): i32 {
  let field = mp4_field(input, at + 4);
  if (field == FREE) {
    return free_field(input, at + 8, to);
  }
  return field;
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
    let field = item_field(input, at, (at + size).min(end));
    let (value, value_end) = box_in(input, at + 8, (at + size).min(end), "data");
    if (field == TRACK && value >= 0 && value + 12 <= value_end) {
      t = Tags { ...t, track: wire.be(input, value + 10, 2) as i32 };
    } else if (field == GENRE_NUMBER && value >= 0 && value + 10 <= value_end) {
      t = with_field(t, GENRE, genre_name(wire.be(input, value + 8, 2) as i32 - 1));
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

### `writing`

`retagged` gives the file with the texts and the numbers of `t`: the title, the artist, the album, the artist of the album, the track number, the year and the genre. A field that is "" or 0 has no frame or comment.

- An MP3 file gets an ID3v2 tag. A tag of the version 2.3 stays at 2.3, with its texts in UTF-16. A tag of the version 2.4 and a new tag have their texts in UTF-8. Each other frame of the tag stays: pictures, lyrics and gains.
- A FLAC file gets a new Vorbis comment block in the place of the old one. Each other comment and each other block stay.

Other formats give None. An ID3v2 tag of the version 2.2 also gives None, and so does a tag with unsynchronisation or a footer. A FLAC file whose blocks go past its end also gives None.

```fern
<<writing>>=
// The bytes of padding after the frames of a new ID3v2 tag.
const PADDING: i32 = 512;

// The frames that `retagged` writes.
function written_ids(): string[] {
  return ["TIT2", "TPE1", "TALB", "TPE2", "TRCK", "TDRC", "TYER", "TCON"];
}

// The comments that `retagged` writes.
function written_names(): string[] {
  return ["TITLE", "ARTIST", "ALBUM", "ALBUMARTIST", "ALBUM ARTIST", "TRACKNUMBER", "DATE", "YEAR", "GENRE"];
}

function has(names: string[], name: string): boolean {
  for n in names {
    if (n == name) {
      return true;
    }
  }
  return false;
}

function appended(own out: u8[], more: [u8]): u8[] {
  for b in more {
    out = out.append(b);
  }
  return out;
}

function bytes_of(text: string): u8[] {
  let out: u8[] = [];
  for i in 0..text.len() {
    out = out.append(text[i]);
  }
  return out;
}

function syncsafe_bytes(n: i32): u8[] {
  return [(n >> 21 & 127) as u8, (n >> 14 & 127) as u8, (n >> 7 & 127) as u8, (n & 127) as u8];
}

// The text as UTF-16 with a byte order mark, little-endian. A byte that is
// not UTF-8 gives U+FFFD.
function utf16_bytes(text: string): u8[] {
  let out: u8[] = [255u8, 254u8];
  let i = 0;
  while (i < text.len()) {
    let (cp, n) = utf8.decode_at(text, i).unwrap_or((65533, 1));
    if (cp >= 65536) {
      out = wire.put_le(out, (55296 + (cp - 65536 >> 10)) as u64, 2);
      out = wire.put_le(out, (56320 + (cp - 65536 & 1023)) as u64, 2);
    } else {
      out = wire.put_le(out, cp as u64, 2);
    }
    i += n;
  }
  return out;
}

// A text frame of ID3v2 of the version `version`.
function text_frame(id: string, text: string, version: i32): u8[] {
  let body = if (version == 4) { appended([3u8], bytes_of(text)) } else { appended([1u8], utf16_bytes(text)) };
  let size = if (version == 4) { syncsafe_bytes(body.len()) } else { wire.put_be([], body.len() as u64, 4) };
  return appended(appended(appended(bytes_of(id), size), [0u8, 0u8]), body);
}

// The frames of the fields of `t` that are not "" or 0.
function field_frames(t: Tags, version: i32): u8[] {
  let out: u8[] = [];
  for pair in [("TIT2", t.title), ("TPE1", t.artist), ("TALB", t.album), ("TPE2", t.album_artist), ("TCON", t.genre)] {
    let (id, text) = pair;
    if (text.len() > 0) {
      out = appended(out, text_frame(id, text, version));
    }
  }
  if (t.track > 0) {
    out = appended(out, text_frame("TRCK", t.track.to_string(), version));
  }
  if (t.year > 0) {
    out = appended(out, text_frame(if (version == 4) { "TDRC" } else { "TYER" }, t.year.to_string(), version));
  }
  return out;
}

// The frames from `from` to `end` of a tag of the version `version`, but
// for those that `retagged` writes. Padding ends the frames.
function kept_frames(input: [u8], from: i32, end: i32, version: i32): u8[] {
  let written = written_ids();
  let out: u8[] = [];
  let at = from;
  while (at + 10 <= end) {
    let id = frame_name(input, at, 4);
    let size = if (version == 4) { syncsafe(input, at + 4) } else { wire.be(input, at + 4, 4) as i32 };
    if (id.len() == 0 || size <= 0 || at + 10 + size > end) {
      return out;
    }
    if (!has(written, id)) {
      out = appended(out, input[at:at + 10 + size]);
    }
    at += 10 + size;
  }
  return out;
}

// An ID3v2 tag of the version `version` with the frames `frames`.
function id3_tag(frames: [u8], version: i32): u8[] {
  let out = appended(appended(bytes_of("ID3"), [version as u8, 0u8, 0u8]), syncsafe_bytes(frames.len() + PADDING));
  out = appended(out, frames);
  for _ in 0..PADDING {
    out = out.append(0u8);
  }
  return out;
}

function retagged_mp3(input: [u8], t: Tags): Option[u8[]] {
  if (!wire.tag(input, 0, "ID3")) {
    if (input.len() < 2 || input[0] != 255u8 || (input[1] & 224u8) != 224u8) {
      return None;
    }
    return Some(appended(id3_tag(field_frames(t, 4), 4), input));
  }
  if (input.len() < 10) {
    return None;
  }
  let version = input[3] as i32;
  let flags = input[5] as i32;
  let end = 10 + syncsafe(input, 6);
  if (version < 3 || version > 4 || (flags & 144) != 0 || end > input.len()) {
    return None;
  }
  let frames = appended(field_frames(t, version), kept_frames(input, frames_start(input, 0, end), end, version));
  return Some(appended(id3_tag(frames, version), input[end:]));
}

// A Vorbis comment: its length, then its text.
function comment_bytes(text: string): u8[] {
  return appended(wire.put_le([], text.len() as u64, 4), bytes_of(text));
}

// The comments of `t` that are not "" or 0, as `NAME=value`.
function field_comments(t: Tags): string[] {
  let out: string[] = [];
  for pair in [("TITLE", t.title), ("ARTIST", t.artist), ("ALBUM", t.album), ("ALBUMARTIST", t.album_artist), ("GENRE", t.genre)] {
    let (name, text) = pair;
    if (text.len() > 0) {
      out = out.append(name + "=" + text);
    }
  }
  if (t.track > 0) {
    out = out.append("TRACKNUMBER=" + t.track.to_string());
  }
  if (t.year > 0) {
    out = out.append("DATE=" + t.year.to_string());
  }
  return out;
}

// The vendor and the comments of the Vorbis comment block from `from` to
// `to`, but for those that `retagged` writes.
function kept_comments(input: [u8], from: i32, to: i32): (u8[], string[]) {
  let kept: string[] = [];
  let vendor_end = if (from + 8 > to) { -1 } else { from + 4 + wire.le(input, from, 4) as i32 };
  if (vendor_end < from + 4 || vendor_end + 4 > to) {
    return (bytes_of("Fern"), kept);
  }
  let vendor = appended([], input[from + 4:vendor_end]);
  let count = wire.le(input, vendor_end, 4) as i32;
  let at = vendor_end + 4;
  let written = written_names();
  for _ in 0..count {
    if (at + 4 > to) {
      return (vendor, kept);
    }
    let n = wire.le(input, at, 4) as i32;
    if (n < 0 || at + 4 + n > to) {
      return (vendor, kept);
    }
    if (!is_written(input, at + 4, at + 4 + n, written)) {
      kept = kept.append(utf8.from_bytes_lossy(input[at + 4:at + 4 + n]));
    }
    at += 4 + n;
  }
  return (vendor, kept);
}

// The comment from `from` to `to` has a name of `written`.
function is_written(input: [u8], from: i32, to: i32, written: string[]): boolean {
  let eq = from;
  while (eq < to && input[eq] != b'=') {
    eq += 1;
  }
  return eq > from && eq < to && has(written, utf8_text(input, from, eq).to_upper());
}

// A Vorbis comment block of FLAC: its type 4, its size, its vendor and its
// comments.
function comment_block(vendor: [u8], comments: string[]): u8[] {
  let body = appended(wire.put_le([], vendor.len() as u64, 4), vendor);
  body = wire.put_le(body, comments.len() as u64, 4);
  for c in comments {
    body = appended(body, comment_bytes(c));
  }
  return appended(appended([4u8], wire.put_be([], body.len() as u64, 3)), body);
}

// The block of FLAC at `at` with the comments of `t` when it is the
// Vorbis comment block, the byte after it, and its flag of the last block.
// A block that goes past the end gives no bytes and -1.
function flac_block(input: [u8], at: i32, t: Tags): (u8[], i32, boolean) {
  if (at + 4 > input.len()) {
    return ([], -1, true);
  }
  let head = input[at] as i32;
  let next = at + 4 + wire.be(input, at + 1, 3) as i32;
  if (next > input.len()) {
    return ([], -1, true);
  }
  if ((head & 127) != 4) {
    return (appended([(head & 127) as u8], input[at + 1:next]), next, (head & 128) != 0);
  }
  let (vendor, kept) = kept_comments(input, at + 4, next);
  let all = field_comments(t);
  for c in kept {
    all = all.append(c);
  }
  return (comment_block(vendor, all), next, (head & 128) != 0);
}

// The blocks with a new Vorbis comment block after the first one, when
// they have no such block.
function with_comment_block(own blocks: u8[][], t: Tags): u8[][] {
  for b in blocks {
    if ((b[0] & 127u8) == 4u8) {
      return blocks;
    }
  }
  let out: u8[][] = [];
  for i in 0..blocks.len() {
    out = out.append(blocks[i]);
    if (i == 0) {
      out = out.append(comment_block(bytes_of("Fern"), field_comments(t)));
    }
  }
  return out;
}

// The FLAC file with the comments of `t` in its Vorbis comment block. A
// file with no such block gets one after its first block. The last block
// keeps the flag of the last block.
function retagged_flac(input: [u8], t: Tags): Option[u8[]] {
  let blocks: u8[][] = [];
  let at = 4;
  let last = false;
  while (!last) {
    let (block, next, end) = flac_block(input, at, t);
    if (next < 0) {
      return None;
    }
    blocks = blocks.append(block);
    at = next;
    last = end;
  }
  let all = with_comment_block(blocks, t);
  let out = bytes_of("fLaC");
  for i in 0..all.len() {
    let b = all[i];
    if (i == all.len() - 1) {
      b = b.with(0, b[0] | 128u8);
    }
    out = appended(out, b);
  }
  return Some(appended(out, input[at:]));
}

// The file `input` with the fields of `t` in its tags, or None for a
// format or a tag that this module does not write.
pub function retagged(input: [u8], t: Tags): Option[u8[]] {
  if (wire.tag(input, 0, "fLaC")) {
    return retagged_flac(input, t);
  }
  return retagged_mp3(input, t);
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
