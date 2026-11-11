# Java class files

This module reads a Java class file. A class file holds one class: its name, its parent, its interfaces, its fields and its methods. A tool that writes JNI bindings reads the class files of `android.jar` with it (#14737). It is pure code over a byte array.

## Model

A class file starts with the magic `CAFEBABE` and a version. A constant pool follows. The pool holds each name, each descriptor and each constant of the class. The other parts of the file give an index into it. Then come the access flags, the class, its parent, its interfaces, its fields and its methods.

`parse` reads a class file into a `Class`. A name of a class has `/` between its parts, as in `java/lang/String`. The parent of `java/lang/Object` is the empty text.

A `Field` and a `Method` each have a name, a descriptor and access flags. The constants `PUBLIC`, `STATIC` and the others are the bits of the flags, and `has` tests one bit. A constructor is a method with the name `<init>`. A field that is a compile-time constant has its value in `constant`.

A descriptor is the type of a field or of a method in a short text. `I` is `int`, `Ljava/lang/String;` is a class and `[J` is an array of `long`. `(ILjava/lang/String;)V` is a method that takes an `int` and a `String` and returns nothing. `field_type` reads the descriptor of a field, and `signature` reads the descriptor of a method.

A `Type` holds one type of a descriptor. `base` is the letter of the descriptor. For a class, `base` is `L` and `class` is the name. `dims` is the number of array dimensions.

## Contracts

`parse` refuses bytes that do not start with the magic, and a constant with an unknown tag. It refuses an index that does not point to a constant of the correct kind. It also refuses a file that ends before the last method. The error gives the byte offset of the problem.

`parse` reads the attribute `ConstantValue` of a field and no other attribute. It does not read the code of a method. It does not read the attributes of the class, so the bytes after the last method are not checked.

A class file holds text in modified UTF-8. `parse` gives each text as UTF-8, and it refuses a text that is not modified UTF-8.

`field_type` and `signature` give None for a text that is not a descriptor. They do not limit the number of array dimensions.

## Executable example

```fern test name=std-classfile-example
import "std/classfile";
import "std/option";

function main(): i32 {
  let Some(sig) = classfile.signature("(I[Ljava/lang/String;)J") else {
    return 1;
  };
  if (sig.params.len() != 2 || sig.result.base != "J") {
    return 2;
  }
  if (sig.params[1].class != "java/lang/String" || sig.params[1].dims != 1) {
    return 3;
  }
  return 0;
}
```

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<pub struct ClassError>>

<<access flags>>

<<pub struct Class>>

<<descriptors>>

<<bytes>>

<<the constant pool>>

<<members>>

<<pub function parse>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module reads a Java class file: the names, the fields and the
// methods of one class.
import "core/cmp";
import "core/error";
import "std/option";
import "std/utf8";
```

### `pub struct ClassError`

```fern
<<pub struct ClassError>>=
pub struct ClassError { offset: i32, reason: string }

impl error.Error for ClassError {}

impl cmp.Display for ClassError {}

pub function (e: ClassError) message(): string {
  return "classfile: " + e.reason + " at byte " + e.offset.to_string();
}

pub function (e: ClassError) to_string(): string {
  return e.message();
}
```

### `access flags`

```fern
<<access flags>>=
pub const PUBLIC: i32 = 0x0001;

pub const PRIVATE: i32 = 0x0002;

pub const PROTECTED: i32 = 0x0004;

pub const STATIC: i32 = 0x0008;

pub const FINAL: i32 = 0x0010;

// A method that the compiler made to call a method with a narrower result.
pub const BRIDGE: i32 = 0x0040;

pub const VARARGS: i32 = 0x0080;

pub const INTERFACE: i32 = 0x0200;

pub const ABSTRACT: i32 = 0x0400;

// A member that is not in the source of the class.
pub const SYNTHETIC: i32 = 0x1000;

pub const ANNOTATION: i32 = 0x2000;

pub const ENUM: i32 = 0x4000;

// True when the access flags `access` have the bit `flag`.
pub function has(access: i32, flag: i32): boolean {
  return (access & flag) != 0;
}
```

### `pub struct Class`

```fern
<<pub struct Class>>=
// The value of a field that is a compile-time constant. A `boolean`, a
// `byte`, a `char` and a `short` are each an `Int`.
pub enum Constant { Int(i32), Long(i64), Float(f32), Double(f64), Text(string) }

pub struct Field { name: string, descriptor: string, access: i32, constant: Option[Constant] }

pub struct Method { name: string, descriptor: string, access: i32 }

// One class. `version` is the major version of the class file: 52 is
// Java 8. `parent` is empty for java/lang/Object.
pub struct Class { name: string, parent: string, interfaces: string[], access: i32, version: i32, fields: Field[], methods: Method[] }
```

### `descriptors`

```fern
<<descriptors>>=
// One type of a descriptor. `base` is the letter of the descriptor: "V",
// "Z", "B", "C", "S", "I", "J", "F", "D", or "L" for a class. `class` is
// the name of the class. `dims` is the number of array dimensions.
pub struct Type { base: string, class: string, dims: i32 }

pub struct Signature { params: Type[], result: Type }

// The letter of a primitive type, or "" for a byte that is not one.
function primitive(c: u8): string {
  return match (c as i32) {
    90 => "Z",
    66 => "B",
    67 => "C",
    83 => "S",
    73 => "I",
    74 => "J",
    70 => "F",
    68 => "D",
    _ => "",
  };
}

// The type that starts at byte `at` of `d`, and the offset after it.
function type_at(d: [u8], at: i32): Option[(Type, i32)] {
  let i = at;
  while (i < d.len() && d[i] == 91 as u8) {
    i += 1;
  }
  if (i >= d.len()) {
    return None;
  }
  let dims = i - at;
  let base = primitive(d[i]);
  if (base.len() > 0) {
    return Some((Type { base: base, class: "", dims: dims }, i + 1));
  }
  let Some((name, next)) = class_at(d, i) else {
    return None;
  };
  return Some((Type { base: "L", class: name, dims: dims }, next));
}

// The name of the class whose "L" is at byte `at` of `d`, and the offset
// after its ";".
function class_at(d: [u8], at: i32): Option[(string, i32)] {
  let end = at + 1;
  while (end < d.len() && d[end] != 59 as u8) {
    end += 1;
  }
  if (d[at] != 76 as u8 || end >= d.len() || end == at + 1) {
    return None;
  }
  let Ok(name) = utf8.from_bytes(d[at + 1:end]) else {
    return None;
  };
  return Some((name, end + 1));
}

// The type of the descriptor `descriptor` of a field.
pub function field_type(descriptor: string): Option[Type] {
  let d = descriptor.as_bytes();
  let Some((t, next)) = type_at(d, 0) else {
    return None;
  };
  if (next != d.len()) {
    return None;
  }
  return Some(t);
}

// The parameters and the result of the descriptor `descriptor` of a
// method.
pub function signature(descriptor: string): Option[Signature] {
  let d = descriptor.as_bytes();
  if (d.len() < 3 || d[0] != 40 as u8) {
    return None;
  }
  let params: Type[] = [];
  let i = 1;
  while (i < d.len() && d[i] != 41 as u8) {
    let Some((t, next)) = type_at(d, i) else {
      return None;
    };
    params = params.append(t);
    i = next;
  }
  if (i + 2 == d.len() && d[i + 1] == 86 as u8) {
    return Some(Signature { params: params, result: Type { base: "V", class: "", dims: 0 } });
  }
  let Some((result, end)) = type_at(d, i + 1) else {
    return None;
  };
  if (end != d.len()) {
    return None;
  }
  return Some(Signature { params: params, result: result });
}
```

### `bytes`

A class file holds each number with its high byte first.

```fern
<<bytes>>=
function be16(b: [u8], i: i32): i32 {
  return b[i] as i32 << 8 | b[i + 1] as i32;
}

function be32(b: [u8], i: i32): i32 {
  return be16(b, i) << 16 | be16(b, i + 2);
}

function be64(b: [u8], i: i32): i64 {
  return be32(b, i) as i64 << 32 | be32(b, i + 4) as i64 & 0xffffffff;
}

// Checks that `b` has `n` bytes at `at`.
function need(b: [u8], at: i32, n: i32): Result[i32, ClassError] {
  if (n < 0 || at > b.len() - n) {
    return Err(ClassError { offset: at, reason: "the bytes end inside the class" });
  }
  return Ok(at);
}

// True when two surrogates of three bytes each start at byte `i` of `m`.
function pair_at(m: [u8], i: i32): boolean {
  if (i + 5 >= m.len() || m[i] != 0xed as u8 || m[i + 3] != 0xed as u8) {
    return false;
  }
  return (m[i + 1] as i32 & 0xf0) == 0xa0 && (m[i + 4] as i32 & 0xf0) == 0xb0;
}

// The UTF-8 text of the modified UTF-8 bytes `m`, which start at byte
// `at` of the file. Modified UTF-8 holds a NUL as two bytes, and a
// character above U+FFFF as two surrogates of three bytes each.
function standard(m: [u8], at: i32): Result[string, ClassError] {
  let out: u8[] = [];
  let i = 0;
  while (i < m.len()) {
    let c = m[i] as i32;
    if (c == 0xc0 && i + 1 < m.len() && m[i + 1] == 0x80 as u8) {
      out = out.append(0 as u8);
      i += 2;
    } else if (pair_at(m, i)) {
      let high = (m[i + 1] as i32 & 0x0f) << 6 | m[i + 2] as i32 & 0x3f;
      let low = (m[i + 4] as i32 & 0x0f) << 6 | m[i + 5] as i32 & 0x3f;
      let cp = 0x10000 + (high << 10 | low);
      out = out.append((0xf0 | cp >> 18) as u8);
      out = out.append((0x80 | cp >> 12 & 0x3f) as u8);
      out = out.append((0x80 | cp >> 6 & 0x3f) as u8);
      out = out.append((0x80 | cp & 0x3f) as u8);
      i += 6;
    } else {
      out = out.append(m[i]);
      i += 1;
    }
  }
  let Ok(text) = utf8.from_bytes(out) else {
    return Err(ClassError { offset: at, reason: "the text is not modified UTF-8" });
  };
  return Ok(text);
}
```

### `the constant pool`

Constant `i` has the tag `tags[i]`, and its bytes start at `at[i]`, after the tag. The pool has no constant 0. A `long` and a `double` each take two indexes, and the second index has the tag 0.

```fern
<<the constant pool>>=
struct Pool { tags: i32[], at: i32[], end: i32 }

const TAG_TEXT: i32 = 1;

const TAG_INT: i32 = 3;

const TAG_FLOAT: i32 = 4;

const TAG_LONG: i32 = 5;

const TAG_DOUBLE: i32 = 6;

const TAG_CLASS: i32 = 7;

const TAG_STRING: i32 = 8;

// The number of bytes after the tag `tag` for a constant of a fixed size,
// or -1.
function fixed_size(tag: i32): i32 {
  // The index is the tag. Tags 9 to 12 and 15 to 20 are references that
  // this module does not read.
  let sizes = [-1, -1, -1, 4, 4, 8, 8, 2, 2, 4, 4, 4, 4, -1, -1, 3, 2, 4, 4, 2, 2];
  if (tag < 0 || tag >= sizes.len()) {
    return -1;
  }
  return sizes[tag];
}

// The number of bytes after the tag of the constant whose tag is at `p`.
function constant_size(b: [u8], p: i32): Result[i32, ClassError] {
  let tag = b[p] as i32;
  if (tag == TAG_TEXT) {
    let head = need(b, p + 1, 2)?;
    let size = 2 + be16(b, head);
    let all = need(b, head, size)?;
    return Ok(size);
  }
  let size = fixed_size(tag);
  if (size < 0) {
    return Err(ClassError { offset: p, reason: "the constant has an unknown tag" });
  }
  let all = need(b, p + 1, size)?;
  return Ok(size);
}

function read_pool(b: [u8]): Result[Pool, ClassError] {
  let head = need(b, 0, 10)?;
  if (be16(b, 0) != 0xcafe || be16(b, 2) != 0xbabe) {
    return Err(ClassError { offset: 0, reason: "the magic is not CAFEBABE" });
  }
  let count = be16(b, 8);
  let tags: i32[] = [0];
  let at: i32[] = [0];
  let p = 10;
  while (tags.len() < count) {
    let tag_at = need(b, p, 1)?;
    let size = constant_size(b, p)?;
    let tag = b[p] as i32;
    tags = tags.append(tag);
    at = at.append(p + 1);
    if (tag == TAG_LONG || tag == TAG_DOUBLE) {
      tags = tags.append(0);
      at = at.append(0);
    }
    p += 1 + size;
  }
  return Ok(Pool { tags: tags, at: at, end: p });
}

// The offset of the bytes of constant `index`, which must have the tag
// `tag`. `from` is the offset of the index in the file.
function entry(pool: Pool, index: i32, tag: i32, from: i32): Result[i32, ClassError] {
  if (index <= 0 || index >= pool.tags.len() || pool.tags[index] != tag) {
    return Err(ClassError { offset: from, reason: "the index does not point to a constant of the correct kind" });
  }
  return Ok(pool.at[index]);
}

function text(b: [u8], pool: Pool, index: i32, from: i32): Result[string, ClassError] {
  let p = entry(pool, index, TAG_TEXT, from)?;
  return standard(b[p + 2:p + 2 + be16(b, p)], p + 2);
}

function class_name(b: [u8], pool: Pool, index: i32, from: i32): Result[string, ClassError] {
  let p = entry(pool, index, TAG_CLASS, from)?;
  return text(b, pool, be16(b, p), p);
}

// The value of constant `index` for the attribute ConstantValue.
function value(b: [u8], pool: Pool, index: i32, from: i32): Result[Constant, ClassError] {
  let tag = 0;
  if (index > 0 && index < pool.tags.len()) {
    tag = pool.tags[index];
  }
  let p = entry(pool, index, tag, from)?;
  if (tag == TAG_INT) {
    return Ok(Int(be32(b, p)));
  }
  if (tag == TAG_FLOAT) {
    return Ok(Float(f32_from_bits(be32(b, p))));
  }
  if (tag == TAG_LONG) {
    return Ok(Long(be64(b, p)));
  }
  if (tag == TAG_DOUBLE) {
    return Ok(Double(f64_from_bits(be64(b, p))));
  }
  if (tag == TAG_STRING) {
    return Ok(Text(text(b, pool, be16(b, p), p)?));
  }
  return Err(ClassError { offset: from, reason: "the index does not point to a constant of the correct kind" });
}
```

### `members`

A field and a method have the same layout: the access flags, the name, the descriptor and a list of attributes. An attribute is a name, a length of 4 bytes and that number of bytes.

```fern
<<members>>=
// One field or one method. `constant` is the index of the value of the
// attribute ConstantValue, or 0. `next` is the offset after the member.
struct Member { name: string, descriptor: string, access: i32, constant: i32, constant_at: i32, next: i32 }

function member(b: [u8], pool: Pool, at: i32): Result[Member, ClassError] {
  let p = need(b, at, 8)?;
  let name = text(b, pool, be16(b, p + 2), p + 2)?;
  let descriptor = text(b, pool, be16(b, p + 4), p + 4)?;
  let constant = 0;
  let constant_at = 0;
  let q = p + 8;
  for k in 0..be16(b, p + 6) {
    let head = need(b, q, 6)?;
    let attribute = text(b, pool, be16(b, q), q)?;
    let size = be32(b, q + 2);
    let body = need(b, q + 6, size)?;
    if (attribute == "ConstantValue" && size == 2) {
      constant = be16(b, body);
      constant_at = body;
    }
    q = body + size;
  }
  return Ok(Member { name: name, descriptor: descriptor, access: be16(b, p), constant: constant, constant_at: constant_at, next: q });
}

function field(b: [u8], pool: Pool, m: Member): Result[Field, ClassError] {
  if (m.constant == 0) {
    return Ok(Field { name: m.name, descriptor: m.descriptor, access: m.access, constant: None });
  }
  let v = value(b, pool, m.constant, m.constant_at)?;
  return Ok(Field { name: m.name, descriptor: m.descriptor, access: m.access, constant: Some(v) });
}
```

### `pub function parse`

```fern
<<pub function parse>>=
// The names of the `count` classes whose indexes start at `at`.
function names(b: [u8], pool: Pool, at: i32, count: i32): Result[string[], ClassError] {
  let out: string[] = [];
  let first = need(b, at, 2 * count)?;
  for k in 0..count {
    out = out.append(class_name(b, pool, be16(b, at + 2 * k), at + 2 * k)?);
  }
  return Ok(out);
}

// The members of the list whose count is at `at`. A class file has a list
// of fields and then a list of methods.
function members(b: [u8], pool: Pool, at: i32): Result[Member[], ClassError] {
  let out: Member[] = [];
  let q = need(b, at, 2)? + 2;
  for k in 0..be16(b, at) {
    let m = member(b, pool, q)?;
    q = m.next;
    out = out.append(m);
  }
  return Ok(out);
}

// The offset after the list `list`, which starts at `at`.
function after(list: Member[], at: i32): i32 {
  if (list.len() == 0) {
    return at + 2;
  }
  return list[list.len() - 1].next;
}

function fields_of(b: [u8], pool: Pool, list: Member[]): Result[Field[], ClassError] {
  let out: Field[] = [];
  for m in list {
    out = out.append(field(b, pool, m)?);
  }
  return Ok(out);
}

// The name of the parent whose index is at `at`. The index 0 is for
// java/lang/Object, which has no parent.
function parent_name(b: [u8], pool: Pool, at: i32): Result[string, ClassError] {
  if (be16(b, at) == 0) {
    return Ok("");
  }
  return class_name(b, pool, be16(b, at), at);
}

// The class of the class file `bytes`.
pub function parse(bytes: [u8]): Result[Class, ClassError] {
  let pool = read_pool(bytes)?;
  let p = need(bytes, pool.end, 8)?;
  let name = class_name(bytes, pool, be16(bytes, p + 2), p + 2)?;
  let parent = parent_name(bytes, pool, p + 4)?;
  let count = be16(bytes, p + 6);
  let interfaces = names(bytes, pool, p + 8, count)?;
  let field_list = members(bytes, pool, p + 8 + 2 * count)?;
  let fields = fields_of(bytes, pool, field_list)?;
  let methods: Method[] = [];
  for m in members(bytes, pool, after(field_list, p + 8 + 2 * count))? {
    methods = methods.append(Method { name: m.name, descriptor: m.descriptor, access: m.access });
  }
  return Ok(Class { name: name, parent: parent, interfaces: interfaces, access: be16(bytes, p), version: be16(bytes, 6), fields: fields, methods: methods });
}
```
