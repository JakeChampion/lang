# Java Native Interface calls

This module calls JNI methods from Fern.

## Model

The JNI environment supplies a table of function pointers. Typed helpers select table entries and marshal arguments into JNI call forms.

## Contracts

Environment pointers, object references, method identifiers, and field identifiers must belong to the active JNI context. Pointer-sized integers do not validate that context.

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<module introduction>>

<<2 pub function method_ptr(env: usize, index: i32): usize>>

<<3 pub function get_string_utf_length(env: usize, jstr: usize): i32>>

<<4 pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module calls JNI methods from Fern.
import "std/utf8";
```

### `2 pub function method_ptr(env: usize, index: i32): usize`

```fern
<<2 pub function method_ptr(env: usize, index: i32): usize>>=
pub function method_ptr(env: usize, index: i32): usize {
  unsafe {
    let table: usize = __load_ptr(env);
    return __load_ptr(table + (index * 8) as usize);
  }
}

pub function call0(env: usize, index: i32): usize {
  unsafe {
    return __c_call1(method_ptr(env, index), env);
  }
}

pub function call1(env: usize, index: i32, a0: usize): usize {
  unsafe {
    return __c_call2(method_ptr(env, index), env, a0);
  }
}

pub function call2(env: usize, index: i32, a0: usize, a1: usize): usize {
  unsafe {
    return __c_call3(method_ptr(env, index), env, a0, a1);
  }
}

pub function call3(env: usize, index: i32, a0: usize, a1: usize, a2: usize): usize {
  unsafe {
    return __c_call4(method_ptr(env, index), env, a0, a1, a2);
  }
}

pub function call1_f32(env: usize, index: i32, a0: usize): f32 {
  unsafe {
    return __c_call2_f32(method_ptr(env, index), env, a0);
  }
}

pub function call1_f64(env: usize, index: i32, a0: usize): f64 {
  unsafe {
    return __c_call2_f64(method_ptr(env, index), env, a0);
  }
}

pub function call2_f32(env: usize, index: i32, a0: usize, a1: usize): f32 {
  unsafe {
    return __c_call3_f32(method_ptr(env, index), env, a0, a1);
  }
}

pub function call2_f64(env: usize, index: i32, a0: usize, a1: usize): f64 {
  unsafe {
    return __c_call3_f64(method_ptr(env, index), env, a0, a1);
  }
}

pub function call3_f32(env: usize, index: i32, a0: usize, a1: usize, a2: usize): f32 {
  unsafe {
    return __c_call4_f32(method_ptr(env, index), env, a0, a1, a2);
  }
}

pub function call3_f64(env: usize, index: i32, a0: usize, a1: usize, a2: usize): f64 {
  unsafe {
    return __c_call4_f64(method_ptr(env, index), env, a0, a1, a2);
  }
}

pub const FIND_CLASS: i32 = 6;

pub const GET_METHOD_ID: i32 = 33;

pub const GET_FIELD_ID: i32 = 94;

pub const GET_STATIC_METHOD_ID: i32 = 113;

pub const GET_STATIC_FIELD_ID: i32 = 144;

pub const NEW_STRING: i32 = 163;

pub const NEW_STRING_UTF: i32 = 167;

pub const GET_STRING_UTF_LENGTH: i32 = 168;

pub const GET_STRING_UTF_CHARS: i32 = 169;

pub const RELEASE_STRING_UTF_CHARS: i32 = 170;

// A buffer from cstr, jvalues or jvalue_alloc has its allocation size in the
// 8 bytes before the pointer, so that free_buffer can free it.
function alloc_buffer(n: i32): usize {
  unsafe {
    let base: usize = __alloc(n + 8);
    __store_i64(base, (n + 8) as i64);
    return base + 8;
  }
}

// Frees a buffer from cstr, jvalues or jvalue_alloc.
pub function free_buffer(buf: usize): void {
  unsafe {
    let base: usize = buf - 8;
    __free(base, __load_i64(base) as i32);
  }
}

// A NUL-terminated copy of `s`. Free it with free_buffer.
pub function cstr(s: string): usize {
  unsafe {
    let n: i32 = s.len();
    let buf: usize = alloc_buffer(n + 4);
    let i = 0;
    while (i + 4 <= n) {
      let w: i32 = s[i] as i32 | s[i + 1] as i32 << 8 | s[i + 2] as i32 << 16 | s[i + 3] as i32 << 24;
      __store_i32(buf + i as usize, w);
      i += 4;
    }
    let base: i32 = i;
    let t = 0;
    let sh = 0;
    while (i < n) {
      t = t | s[i] as i32 << sh;
      sh += 8;
      i += 1;
    }
    __store_i32(buf + base as usize, t);
    return buf;
  }
}

pub function get_version(env: usize): i32 {
  return call0(env, 4) as i32;
}

// The class with the JNI name `name`, such as "java/lang/String".
pub function find_class(env: usize, name: string): usize {
  let n: usize = cstr(name);
  let clazz: usize = call1(env, FIND_CLASS, n);
  free_buffer(n);
  return clazz;
}

pub function get_object_class(env: usize, obj: usize): usize {
  return call1(env, 31, obj);
}

// A method or field ID: the table entry `index` called with C copies of
// `name` and `sig`, which it frees after the call.
function member_id(env: usize, index: i32, clazz: usize, name: string, sig: string): usize {
  let n: usize = cstr(name);
  let g: usize = cstr(sig);
  let id: usize = call3(env, index, clazz, n, g);
  free_buffer(g);
  free_buffer(n);
  return id;
}

pub function get_method_id(env: usize, clazz: usize, name: string, sig: string): usize {
  return member_id(env, GET_METHOD_ID, clazz, name, sig);
}

pub function get_field_id(env: usize, clazz: usize, name: string, sig: string): usize {
  return member_id(env, GET_FIELD_ID, clazz, name, sig);
}

pub function get_static_method_id(env: usize, clazz: usize, name: string, sig: string): usize {
  return member_id(env, GET_STATIC_METHOD_ID, clazz, name, sig);
}

pub function get_static_field_id(env: usize, clazz: usize, name: string, sig: string): usize {
  return member_id(env, GET_STATIC_FIELD_ID, clazz, name, sig);
}

pub function new_string_utf(env: usize, bytes: usize): usize {
  return call1(env, NEW_STRING_UTF, bytes);
}

// The code point at byte `i` of `s`, and the number of bytes it takes. A
// byte that does not start a whole character is U+FFFD, and takes one byte.
function next_char(s: string, i: i32): (i32, i32) {
  let b = s[i] as i32;
  if (b < 128) {
    return (b, 1);
  }
  let k = 1;
  let c = 0;
  if (b >= 240 && b < 248) {
    k = 4;
    c = b & 7;
  } else if (b >= 224 && b < 240) {
    k = 3;
    c = b & 15;
  } else if (b >= 192 && b < 224) {
    k = 2;
    c = b & 31;
  }
  if (k == 1 || i + k > s.len()) {
    return (65533, 1);
  }
  for j in 1..k {
    let t = s[i + j] as i32;
    if (t < 128 || t >= 192) {
      return (65533, 1);
    }
    c = c << 6 | t & 63;
  }
  return (c, k);
}

// Writes UTF-16 unit `u` at index `n` of the buffer `units`, in native byte
// order, which is little-endian on every Android ABI.
function put_unit(units: usize, n: i32, u: i32): void {
  unsafe {
    __store_u8(units + (2 * n) as usize, u & 255);
    __store_u8(units + (2 * n + 1) as usize, u >> 8 & 255);
  }
}

// A Java string with the contents of `s`. It goes to NewString as UTF-16, so
// a NUL and a character above U+FFFF keep their meaning: NewStringUTF takes
// modified UTF-8, which spells both differently from Fern. The temporary
// buffer is freed.
pub function new_string(env: usize, s: string): usize {
  unsafe {
    let units: usize = alloc_buffer(2 * s.len() + 2);
    let n = 0;
    let i = 0;
    while (i < s.len()) {
      let (c, used) = next_char(s, i);
      i += used;
      if (c >= 65536) {
        put_unit(units, n, 55296 + (c - 65536 >> 10));
        put_unit(units, n + 1, 56320 + (c - 65536 & 1023));
        n += 2;
      } else {
        put_unit(units, n, c);
        n += 1;
      }
    }
    let jstr: usize = call2(env, NEW_STRING, units, n as usize);
    free_buffer(units);
    return jstr;
  }
}
```

### `3 pub function get_string_utf_length(env: usize, jstr: usize): i32`

```fern
<<3 pub function get_string_utf_length(env: usize, jstr: usize): i32>>=
pub function get_string_utf_length(env: usize, jstr: usize): i32 {
  return call1(env, GET_STRING_UTF_LENGTH, jstr) as i32;
}

pub function get_string_utf_chars(env: usize, jstr: usize, is_copy: usize): usize {
  return call2(env, GET_STRING_UTF_CHARS, jstr, is_copy);
}

pub function release_string_utf_chars(env: usize, jstr: usize, chars: usize): usize {
  return call2(env, RELEASE_STRING_UTF_CHARS, jstr, chars);
}

pub function throw(env: usize, throwable: usize): i32 {
  return call1(env, 13, throwable) as i32;
}

pub function throw_new(env: usize, clazz: usize, message: string): i32 {
  let m: usize = cstr(message);
  let r = call2(env, 14, clazz, m) as i32;
  free_buffer(m);
  return r;
}

pub function exception_occurred(env: usize): usize {
  return call0(env, 15);
}

pub function exception_describe(env: usize): usize {
  return call0(env, 16);
}

pub function exception_clear(env: usize): usize {
  return call0(env, 17);
}

pub function new_global_ref(env: usize, obj: usize): usize {
  return call1(env, 21, obj);
}

pub function delete_global_ref(env: usize, gref: usize): usize {
  return call1(env, 22, gref);
}

pub function delete_local_ref(env: usize, lref: usize): usize {
  return call1(env, 23, lref);
}

pub function new_local_ref(env: usize, ref: usize): usize {
  return call1(env, 25, ref);
}

pub function get_superclass(env: usize, clazz: usize): usize {
  return call1(env, 10, clazz);
}

pub function is_assignable_from(env: usize, sub: usize, sup: usize): i32 {
  return call2(env, 11, sub, sup) as i32;
}

pub function is_same_object(env: usize, a: usize, b: usize): i32 {
  return call2(env, 24, a, b) as i32;
}

pub function alloc_object(env: usize, clazz: usize): usize {
  return call1(env, 27, clazz);
}

pub function is_instance_of(env: usize, obj: usize, clazz: usize): i32 {
  return call2(env, 32, obj, clazz) as i32;
}

pub function get_object_field(env: usize, obj: usize, field: usize): usize {
  return call2(env, 95, obj, field);
}

pub function get_boolean_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 96, obj, field) as i32;
}

pub function get_byte_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 97, obj, field) as i32;
}

pub function get_char_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 98, obj, field) as i32;
}

pub function get_short_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 99, obj, field) as i32;
}

pub function get_int_field(env: usize, obj: usize, field: usize): i32 {
  return call2(env, 100, obj, field) as i32;
}

pub function get_long_field(env: usize, obj: usize, field: usize): usize {
  return call2(env, 101, obj, field);
}

pub function get_float_field(env: usize, obj: usize, field: usize): f32 {
  return call2_f32(env, 102, obj, field);
}

pub function get_double_field(env: usize, obj: usize, field: usize): f64 {
  return call2_f64(env, 103, obj, field);
}

pub function set_object_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 104, obj, field, value);
}

pub function set_boolean_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 105, obj, field, value);
}

pub function set_byte_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 106, obj, field, value);
}

pub function set_char_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 107, obj, field, value);
}

pub function set_short_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 108, obj, field, value);
}

pub function set_int_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 109, obj, field, value);
}

pub function set_long_field(env: usize, obj: usize, field: usize, value: usize): usize {
  return call3(env, 110, obj, field, value);
}

pub function get_static_object_field(env: usize, clazz: usize, field: usize): usize {
  return call2(env, 145, clazz, field);
}
```

### `4 pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32`

```fern
<<4 pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32>>=
pub function get_static_int_field(env: usize, clazz: usize, field: usize): i32 {
  return call2(env, 150, clazz, field) as i32;
}

pub function get_static_long_field(env: usize, clazz: usize, field: usize): usize {
  return call2(env, 151, clazz, field);
}

pub function set_static_long_field(env: usize, clazz: usize, field: usize, value: usize): usize {
  return call3(env, 160, clazz, field, value);
}

pub function get_static_float_field(env: usize, clazz: usize, field: usize): f32 {
  return call2_f32(env, 152, clazz, field);
}

pub function get_static_double_field(env: usize, clazz: usize, field: usize): f64 {
  return call2_f64(env, 153, clazz, field);
}

// A new array of `n` references of class `clazz`, each set to `init`.
pub function new_object_array(env: usize, n: i32, clazz: usize, init: usize): usize {
  return call3(env, 172, n as usize, clazz, init);
}

pub function set_object_array_element(env: usize, array: usize, index: i32, value: usize): usize {
  return call3(env, 174, array, index as usize, value);
}

// A String[] of `items`. The local references of the strings are deleted.
pub function string_array(env: usize, items: string[]): usize {
  let array: usize = new_object_array(env, items.len(), find_class(env, "java/lang/String"), 0 as usize);
  for k in 0..items.len() {
    let s: usize = new_string(env, items[k]);
    let ignored: usize = set_object_array_element(env, array, k, s);
    let deleted: usize = delete_local_ref(env, s);
  }
  return array;
}

pub function get_array_length(env: usize, array: usize): i32 {
  return call1(env, 171, array) as i32;
}

pub function get_object_array_element(env: usize, array: usize, index: i32): usize {
  return call2(env, 173, array, index as usize);
}

// A jvalue array of `args`. Free it with free_buffer.
pub function jvalues(args: usize[]): usize {
  unsafe {
    let n: i32 = args.len();
    let buf: usize = alloc_buffer(n * 8);
    let i = 0;
    while (i < n) {
      __store_i64(buf + (i * 8) as usize, args[i] as i64);
      i += 1;
    }
    return buf;
  }
}

// A jvalue array of `n` zeros. Free it with free_buffer.
pub function jvalue_alloc(n: i32): usize {
  unsafe {
    let buf: usize = alloc_buffer(n * 8);
    let i = 0;
    while (i < n) {
      __store_i64(buf + (i * 8) as usize, 0 as i64);
      i += 1;
    }
    return buf;
  }
}

pub function jvalue_set_int(args: usize, idx: i32, v: usize): usize {
  unsafe {
    __store_i64(args + (idx * 8) as usize, v as i64);
    return args;
  }
}

pub function jvalue_set_long(args: usize, idx: i32, v: i64): usize {
  unsafe {
    __store_i64(args + (idx * 8) as usize, v);
    return args;
  }
}

pub function jvalue_set_f32(args: usize, idx: i32, x: f32): usize {
  unsafe {
    __store_i32(args + (idx * 8) as usize, f32_bits(x));
    return args;
  }
}

pub function jvalue_set_f64(args: usize, idx: i32, d: f64): usize {
  unsafe {
    __store_i64(args + (idx * 8) as usize, f64_bits(d));
    return args;
  }
}

// A new object of `clazz`, made by the constructor `ctor` with the jvalue
// array `args`.
pub function new_object_a(env: usize, clazz: usize, ctor: usize, args: usize): usize {
  return call3(env, 30, clazz, ctor, args);
}

pub function call_object_method_a(env: usize, obj: usize, method: usize, args: usize): usize {
  return call3(env, 36, obj, method, args);
}

pub function call_boolean_method_a(env: usize, obj: usize, method: usize, args: usize): i32 {
  return call3(env, 39, obj, method, args) as i32;
}

pub function call_int_method_a(env: usize, obj: usize, method: usize, args: usize): i32 {
  return call3(env, 51, obj, method, args) as i32;
}

pub function call_long_method_a(env: usize, obj: usize, method: usize, args: usize): usize {
  return call3(env, 54, obj, method, args);
}

pub function call_float_method_a(env: usize, obj: usize, method: usize, args: usize): f32 {
  return call3_f32(env, 57, obj, method, args);
}

pub function call_double_method_a(env: usize, obj: usize, method: usize, args: usize): f64 {
  return call3_f64(env, 60, obj, method, args);
}

pub function call_void_method_a(env: usize, obj: usize, method: usize, args: usize): usize {
  return call3(env, 63, obj, method, args);
}

pub function call_static_object_method_a(env: usize, clazz: usize, method: usize, args: usize): usize {
  return call3(env, 116, clazz, method, args);
}

pub function call_static_int_method_a(env: usize, clazz: usize, method: usize, args: usize): i32 {
  return call3(env, 131, clazz, method, args) as i32;
}

pub function call_static_void_method_a(env: usize, clazz: usize, method: usize, args: usize): usize {
  return call3(env, 143, clazz, method, args);
}

// The text of the Java string `jstr`, or "" for null. JNI gives modified
// UTF-8, and a byte that is not UTF-8 becomes U+FFFD.
pub function string_of(env: usize, jstr: usize): string {
  if (jstr == 0 as usize) {
    return "";
  }
  let n: i32 = get_string_utf_length(env, jstr);
  let chars: usize = get_string_utf_chars(env, jstr, 0 as usize);
  let bytes: u8[] = [];
  unsafe {
    for i in 0..n {
      bytes = bytes.append(__load_u8(chars + i as usize) as u8);
    }
  }
  let released: usize = release_string_utf_chars(env, jstr, chars);
  return utf8.from_bytes_lossy(bytes);
}
```
