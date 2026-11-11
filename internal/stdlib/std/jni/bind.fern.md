# Typed JNI bindings

This module writes a Fern module of typed wrappers for Java classes. A wrapper calls a Java method through `std/jni`. The code of the app then has no class name, method name or type signature, and the checker refuses a wrong argument (#14737).

## Model

`generate` takes the classes that `std/classfile` read, and a list of requests. A `Request` names one class and the members that the app uses. With no member, the request is for each public member of the class. A member is a name, such as `setAction`, or a name with its descriptor, such as `setAction(Ljava/lang/String;)Landroid/content/Intent;`. A constructor has the name `<init>`.

The result is the text of one Fern module. For a class such as `android/content/Intent`, the module has these parts:

- A struct `Intent` that holds the reference to one Java object.
- A struct `IntentClass` that holds the class and the ID of each member. `intent_class` finds them one time, so a call does no lookup by name. `release` deletes the reference to the class.
- A function of `IntentClass` for each method, each constructor and each field.
- A constant for each field that is a compile-time constant.

The members of a class are in the order of their names, so the module does not change with the order of the JAR. A function has the name of its Java member in snake case. A constructor has the name `new`. Two methods of a request can have one name. Each function then also has the types of its parameters, as in `put_extra_string_int`.

The primitive types change as follows. A Java `boolean` is a `boolean`. A `byte`, a `short`, a `char` and an `int` are each an `i32`. A `long` is an `i64`, a `float` is an `f32` and a `double` is an `f64`.

A `String` is a `string`. A class that has a request is its struct. Each other class and each array is a `jni.Object`.

A struct has a function `as_` for each parent class and each interface that has a request. Thus an `Activity` can go where a `Context` is necessary.

## Contracts

Each function gives a `Result`. The error is the exception that the Java code threw, and the function clears that exception.

An object that a function gives is a local reference. The caller deletes it with `jni.delete_local_ref`. A function deletes the local references that it makes for its `string` arguments and results.

`intent_class` makes a global reference to the class, and only `release` deletes it. Make each struct of IDs one time for the app, not one time for each call. `intent_class` finds a class with `FindClass`. On Android, a thread that native code started finds only the classes of the system that way.

`generate` gives an error for a request whose class is not in the classes, and for a member that the class does not have. It also gives an error for two requests whose classes have one simple name. It writes no setter for a field, and no constant for a `float` or a `double`.

## Module assembly

```fern
<<*>>=
<<module introduction>>

<<names>>

<<types>>

<<selection>>

<<the struct of a class>>

<<members>>

<<pub function generate>>
```

### `module introduction`

```fern
<<module introduction>>=
// This module writes a Fern module of typed JNI wrappers for Java classes.
import "std/classfile";
import "std/option";
import "std/string";
import "std/utf8";

// One class and the members to bind. No member is a request for each
// public member. A member is a name, or a name and its descriptor.
pub struct Request { class: string, members: string[] }
```

### `names`

```fern
<<names>>=
const KEYWORDS: string[] = ["as", "boolean", "break", "const", "continue", "defer", "dyn", "else", "enum", "errdefer", "f32", "f32x4", "f64", "f64x2", "false", "for", "function", "i32", "i64", "if", "impl", "import", "let", "loop", "match", "pub", "return", "string", "struct", "trait", "true", "type", "u32", "u64", "u8", "use", "usize", "void", "when", "while"];

function is_upper(c: u8): boolean {
  return c >= 65 as u8 && c <= 90 as u8;
}

function is_lower_or_digit(c: u8): boolean {
  return c >= 97 as u8 && c <= 122 as u8 || c >= 48 as u8 && c <= 57 as u8;
}

// The Java name `name` in snake case: "getURL" gives "get_url". A "$"
// and a "<" or ">" give no letter of their own.
pub function snake(name: string): string {
  let b = name.as_bytes();
  let out: u8[] = [];
  for i in 0..b.len() {
    let c = b[i];
    if (is_upper(c)) {
      if (i > 0 && is_lower_or_digit(b[i - 1])) {
        out = out.append(95 as u8);
      }
      out = out.append(c + 32 as u8);
    } else if (c == 36 as u8) {
      out = out.append(95 as u8);
    } else if (c != 60 as u8 && c != 62 as u8) {
      out = out.append(c);
    }
  }
  return utf8.from_bytes_lossy(out);
}

function upper(name: string): string {
  let out: u8[] = [];
  for c in name.as_bytes() {
    if (c >= 97 as u8 && c <= 122 as u8) {
      out = out.append(c - 32 as u8);
    } else {
      out = out.append(c);
    }
  }
  return utf8.from_bytes_lossy(out);
}

// The part of the class name `class` after its package, with "_" for
// each "$": "android/app/ActivityManager$MemoryInfo" gives
// "ActivityManager_MemoryInfo".
pub function type_name(class: string): string {
  let parts = class.split("/");
  return parts[parts.len() - 1].replace("$", "_");
}

// The class name with "." between its parts, as Java source has it.
function java_name(class: string): string {
  return class.replace("/", ".").replace("$", ".");
}

function has(list: string[], s: string): boolean {
  for x in list {
    if (x == s) {
      return true;
    }
  }
  return false;
}

// `name`, or `name` with "_" at its end until it is not a keyword and is
// not in `used`.
function free_name(name: string, used: string[]): string {
  let out = name;
  while (has(KEYWORDS, out) || has(used, out)) {
    out += "_";
  }
  return out;
}
```

### `types`

`bound` holds the names of the classes that have a request.

```fern
<<types>>=
function is_string(t: classfile.Type): boolean {
  return t.dims == 0 && t.class == "java/lang/String";
}

function is_bound(t: classfile.Type, bound: string[]): boolean {
  return t.dims == 0 && t.base == "L" && has(bound, t.class);
}

// The Fern type of the Java type `t`.
function fern_type(t: classfile.Type, bound: string[]): string {
  if (is_string(t)) {
    return "string";
  }
  if (is_bound(t, bound)) {
    return type_name(t.class);
  }
  if (t.dims > 0 || t.base == "L") {
    return "jni.Object";
  }
  return match (t.base) {
    "V" => "void",
    "Z" => "boolean",
    "J" => "i64",
    "F" => "f32",
    "D" => "f64",
    _ => "i32",
  };
}

// The letter of `t` in jni.RESULTS. An array is an object.
function result_letter(t: classfile.Type): string {
  if (t.dims > 0) {
    return "L";
  }
  return t.base;
}

// The place of the result `t` in the order of jni.RESULTS.
function result_index(t: classfile.Type): i32 {
  let order = ["L", "Z", "B", "C", "S", "I", "J", "F", "D", "V"];
  for i in 0..order.len() {
    if (order[i] == result_letter(t)) {
      return i;
    }
  }
  return 0;
}

// A short name of `t` for the name of an overloaded function.
function short_name(t: classfile.Type): string {
  let names = ["object", "boolean", "byte", "char", "short", "int", "long", "float", "double", "object"];
  let name = names[result_index(t)];
  if (t.base == "L") {
    name = snake(type_name(t.class));
  }
  if (t.dims > 0) {
    return name + "s";
  }
  return name;
}
```

### `selection`

```fern
<<selection>>=
function find(classes: classfile.Class[], name: string): Option[classfile.Class] {
  for c in classes {
    if (c.name == name) {
      return Some(c);
    }
  }
  return None;
}

// True when the request `r` asks for the member `name` with the
// descriptor `descriptor`.
function wanted(r: Request, name: string, descriptor: string): boolean {
  return r.members.len() == 0 || has(r.members, name) || has(r.members, name + descriptor);
}

function is_public(access: i32): boolean {
  return classfile.has(access, classfile.PUBLIC) && !classfile.has(access, classfile.SYNTHETIC);
}

// The places of `keys` in the order of the keys. Two JAR files can hold
// the members of one class in different orders, and the module must not
// change with the JAR.
function sorted(keys: string[]): i32[] {
  let out: i32[] = [];
  for i in 0..keys.len() {
    let at = out.len();
    out = out.append(i);
    while (at > 0 && keys[out[at - 1]] > keys[i]) {
      out = out.with(at, out[at - 1]);
      at -= 1;
    }
    out = out.with(at, i);
  }
  return out;
}

// The methods of the request, in the order of their names and
// descriptors.
function methods_of(c: classfile.Class, r: Request): classfile.Method[] {
  let picked: classfile.Method[] = [];
  let keys: string[] = [];
  for m in c.methods {
    if (is_public(m.access) && m.name != "<clinit>" && wanted(r, m.name, m.descriptor)) {
      picked = picked.append(m);
      keys = keys.append(m.name + m.descriptor);
    }
  }
  let out: classfile.Method[] = [];
  for i in sorted(keys) {
    out = out.append(picked[i]);
  }
  return out;
}

// The fields of the request, in the order of their names.
function fields_of(c: classfile.Class, r: Request): classfile.Field[] {
  let picked: classfile.Field[] = [];
  let keys: string[] = [];
  for f in c.fields {
    if (is_public(f.access) && wanted(r, f.name, f.descriptor)) {
      picked = picked.append(f);
      keys = keys.append(f.name);
    }
  }
  let out: classfile.Field[] = [];
  for i in sorted(keys) {
    out = out.append(picked[i]);
  }
  return out;
}

// True when `c` has a public member that the text `want` of a request
// names.
function has_member(c: classfile.Class, want: string): boolean {
  for m in c.methods {
    if (is_public(m.access) && (m.name == want || m.name + m.descriptor == want)) {
      return true;
    }
  }
  for f in c.fields {
    if (is_public(f.access) && (f.name == want || f.name + f.descriptor == want)) {
      return true;
    }
  }
  return false;
}

// The member of `r` that `c` does not have, or "".
function missing(c: classfile.Class, r: Request): string {
  for want in r.members {
    if (!has_member(c, want)) {
      return want;
    }
  }
  return "";
}

// The parent classes and the interfaces of `c`, as far as `classes` has
// them.
function ancestors(classes: classfile.Class[], c: classfile.Class): string[] {
  let out: string[] = [];
  let todo: string[] = c.interfaces.append(c.parent);
  let next = 0;
  while (next < todo.len()) {
    let name = todo[next];
    next += 1;
    if (name.len() > 0 && !has(out, name)) {
      out = out.append(name);
      if let Some(p) = find(classes, name) {
        todo = todo.concat(p.interfaces).append(p.parent);
      }
    }
  }
  return out;
}
```

### `the struct of a class`

```fern
<<the struct of a class>>=
// One function to write: the Java member and the name of the function.
struct Bound { name: string, java: string, descriptor: string, is_static: boolean, is_field: boolean }

function is_static(access: i32): boolean {
  return classfile.has(access, classfile.STATIC);
}

function count_named(methods: classfile.Method[], name: string): i32 {
  let n = 0;
  for m in methods {
    if (m.name == name) {
      n += 1;
    }
  }
  return n;
}

// The name of the function of the method `m`. `overloaded` adds the
// types of the parameters.
function method_name(m: classfile.Method, overloaded: boolean): string {
  let name = snake(m.name);
  if (m.name == "<init>") {
    name = "new";
  }
  if (!overloaded) {
    return name;
  }
  if let Some(sig) = classfile.signature(m.descriptor) {
    for p in sig.params {
      name += "_" + short_name(p);
    }
  }
  return name;
}

// True for a field whose value is a constant of the module.
function is_constant(f: classfile.Field): boolean {
  return match (f.constant) {
    Some(v) => is_static(f.access) && classfile.has(f.access, classfile.FINAL),
    None => false,
  };
}

// The functions of the class: its methods, then the fields that are not
// constants.
function bound_members(methods: classfile.Method[], fields: classfile.Field[]): Bound[] {
  let out: Bound[] = [];
  // Each struct of IDs has a function with the name `release`.
  let used: string[] = ["release"];
  for m in methods {
    let name = free_name(method_name(m, count_named(methods, m.name) > 1), used);
    used = used.append(name);
    out = out.append(Bound { name: name, java: m.name, descriptor: m.descriptor, is_static: is_static(m.access), is_field: false });
  }
  for f in fields {
    if (!is_constant(f)) {
      let name = free_name(snake(f.name), used);
      used = used.append(name);
      out = out.append(Bound { name: name, java: f.name, descriptor: f.descriptor, is_static: is_static(f.access), is_field: true });
    }
  }
  return out;
}

// The string literal of `s`.
function quoted(s: string): string {
  let out: u8[] = [34 as u8];
  for c in s.as_bytes() {
    if (c == 34 as u8 || c == 92 as u8) {
      out = out.append(92 as u8).append(c);
    } else if (c < 32 as u8) {
      out = out.concat(("\\x" + (c as i32 / 16).to_string() + (c as i32 % 16).to_string_radix(16)).bytes());
    } else {
      out = out.append(c);
    }
  }
  return utf8.from_bytes_lossy(out.append(34 as u8));
}

// The line of the constant of the field `f` of the class with the type
// name `owner`, or "" for a value that has no literal.
function constant_line(owner: string, f: classfile.Field): string {
  let name = upper(snake(owner)) + "_" + f.name;
  return match (f.constant) {
    Some(Int(n)) => int_constant(name, f.descriptor, n),
    Some(Long(n)) => "pub const " + name + ": i64 = " + n.to_string() + ";\n\n",
    Some(Text(s)) => "pub const " + name + ": string = " + quoted(s) + ";\n\n",
    _ => "",
  };
}

function int_constant(name: string, descriptor: string, n: i32): string {
  if (descriptor == "Z") {
    return "pub const " + name + ": boolean = " + (n != 0).to_string() + ";\n\n";
  }
  return "pub const " + name + ": i32 = " + n.to_string() + ";\n\n";
}

// The struct of the objects of the class, and its conversions.
function object_struct(classes: classfile.Class[], c: classfile.Class, bound: string[]): string {
  let t = type_name(c.name);
  let out = "// A " + java_name(c.name) + ".\npub struct " + t + " { ref: usize }\n\n";
  out += "pub function (x: " + t + ") is_null(): boolean {\n  return x.ref == 0 as usize;\n}\n\n";
  out += "pub function (x: " + t + ") object(): jni.Object {\n  return jni.Object { ref: x.ref };\n}\n\n";
  for a in ancestors(classes, c) {
    if (has(bound, a)) {
      let p = type_name(a);
      out += "pub function (x: " + t + ") as_" + snake(p) + "(): " + p + " {\n  return " + p + " { ref: x.ref };\n}\n\n";
    }
  }
  return out;
}

// The struct of the IDs of the class, and the function that finds them.
function class_struct(c: classfile.Class, members: Bound[]): string {
  let t = type_name(c.name);
  let fields = "class: usize";
  let finds = "";
  let values = "class: class";
  for m in members {
    fields += ", id_" + m.name + ": usize";
    values += ", id_" + m.name + ": id_" + m.name;
    finds += "  let id_" + m.name + " = jni.checked_id(e, class, jni." + finder(m) + "(e, class, " + quoted(m.java) + ", " + quoted(m.descriptor) + "))?;\n";
  }
  let out = "// The class " + java_name(c.name) + " and the IDs of its members.\npub struct " + t + "Class { " + fields + " }\n\n";
  out += "pub function " + snake(t) + "_class(e: usize): Result[" + t + "Class, jni.JavaError] {\n";
  out += "  let local = jni.find_class(e, " + quoted(c.name) + ");\n  jni.check(e)?;\n";
  out += "  let class = jni.new_global_ref(e, local);\n  jni.delete_local_ref(e, local);\n";
  out += finds + "  return Ok(" + t + "Class { " + values + " });\n}\n\n";
  out += "// Deletes the reference to the class. Do not use `c` after this call.\n";
  return out + "pub function (c: " + t + "Class) release(e: usize): void {\n  jni.delete_global_ref(e, c.class);\n}\n\n";
}

// The function of std/jni that finds the ID of `m`.
function finder(m: Bound): string {
  if (m.is_field && m.is_static) {
    return "get_static_field_id";
  }
  if (m.is_field) {
    return "get_field_id";
  }
  if (m.is_static) {
    return "get_static_method_id";
  }
  return "get_method_id";
}
```

### `members`

A function puts its arguments in a jvalue array. It calls the entry of the JNI table for the kind of its result. Then it changes the result to its Fern type.

```fern
<<members>>=
// The lines that put argument `i`, of the type `t`, in the jvalue array.
function argument(i: i32, t: classfile.Type, bound: string[]): string {
  let n = i.to_string();
  let a = "a" + n;
  let set = "  jni.jvalue_set_int(args, " + n + ", ";
  if (is_string(t)) {
    return "  let s" + n + " = jni.new_string(e, " + a + ");\n" + set + "s" + n + ");\n";
  }
  if (t.dims > 0 || t.base == "L") {
    return set + a + ".ref);\n";
  }
  return match (t.base) {
    "Z" => set + "jni.jbool(" + a + "));\n",
    "J" => "  jni.jvalue_set_long(args, " + n + ", " + a + ");\n",
    "F" => "  jni.jvalue_set_f32(args, " + n + ", " + a + ");\n",
    "D" => "  jni.jvalue_set_f64(args, " + n + ", " + a + ");\n",
    _ => set + a + " as usize);\n",
  };
}

// The expression that changes the result `got`, of the type `t`, to its
// Fern type. The table gives a small integer in the low bits only.
function converted(t: classfile.Type, bound: string[]): string {
  if (is_bound(t, bound)) {
    return type_name(t.class) + " { ref: got }";
  }
  if (t.dims > 0 || t.base == "L") {
    return "jni.Object { ref: got }";
  }
  // In the order of jni.RESULTS, from "Z" to "D".
  let forms = ["(got & 255 as usize) != 0 as usize", "got as i32 << 24 >> 24", "(got & 65535 as usize) as i32", "got as i32 << 16 >> 16", "got as i32", "got as i64", "got", "got"];
  return forms[result_index(t) - 1];
}

// The function of std/jni that calls the table for the result `t`.
function invoker(t: classfile.Type): string {
  if (t.dims == 0 && t.base == "F") {
    return "jni.invoke_f32";
  }
  if (t.dims == 0 && t.base == "D") {
    return "jni.invoke_f64";
  }
  return "jni.invoke";
}

// The lines after the call: the check for an exception and the result.
function ending(t: classfile.Type, bound: string[]): string {
  if (t.dims == 0 && t.base == "V") {
    return "  jni.check(e)?;\n  return Ok(());\n}\n\n";
  }
  if (is_string(t)) {
    return "  jni.check(e)?;\n  let text = jni.string_of(e, got);\n  jni.delete_local_ref(e, got);\n  return Ok(text);\n}\n\n";
  }
  return "  jni.check(e)?;\n  return Ok(" + converted(t, bound) + ");\n}\n\n";
}

// The start of the call: "let got = " for a result, and the function.
function call_start(t: classfile.Type): string {
  if (t.dims == 0 && t.base == "V") {
    return "  " + invoker(t) + "(e, ";
  }
  return "  let got = " + invoker(t) + "(e, ";
}

// The object or the class that the call of `m` is for.
function target(m: Bound): string {
  if (m.is_static || m.java == "<init>") {
    return "c.class";
  }
  return "self.ref";
}

// The parameters of the function of `m`, after the receiver.
function parameters(t: string, m: Bound, params: classfile.Type[], bound: string[]): string {
  let out = "e: usize";
  if (target(m) == "self.ref") {
    out += ", self: " + t;
  }
  for i in 0..params.len() {
    out += ", a" + i.to_string() + ": " + fern_type(params[i], bound);
  }
  return out;
}

function method_function(c: classfile.Class, m: Bound, sig: classfile.Signature, bound: string[]): string {
  let t = type_name(c.name);
  let result = sig.result;
  let entry = "jni.CALL_METHOD + " + (3 * result_index(result)).to_string();
  if (m.is_static) {
    entry = "jni.CALL_STATIC + " + (3 * result_index(result)).to_string();
  }
  if (m.java == "<init>") {
    result = classfile.Type { base: "L", class: c.name, dims: 0 };
    entry = "30";
  }
  let out = "// " + java_name(c.name) + "." + m.java + m.descriptor + "\n";
  out += "pub function (c: " + t + "Class) " + m.name + "(" + parameters(t, m, sig.params, bound) + "): Result[" + fern_type(result, bound) + ", jni.JavaError] {\n";
  out += "  let args = jni.jvalue_alloc(" + sig.params.len().to_string() + ");\n";
  let frees = "  jni.free_buffer(args);\n";
  for i in 0..sig.params.len() {
    out += argument(i, sig.params[i], bound);
    if (is_string(sig.params[i])) {
      frees += "  jni.delete_local_ref(e, s" + i.to_string() + ");\n";
    }
  }
  out += call_start(result) + entry + ", " + target(m) + ", c.id_" + m.name + ", args);\n";
  return out + frees + ending(result, bound);
}

function field_function(c: classfile.Class, m: Bound, kind: classfile.Type, bound: string[]): string {
  let t = type_name(c.name);
  let entry = "jni.GET_FIELD + " + result_index(kind).to_string();
  if (m.is_static) {
    entry = "jni.GET_STATIC_FIELD + " + result_index(kind).to_string();
  }
  let out = "// " + java_name(c.name) + "." + m.java + "\n";
  out += "pub function (c: " + t + "Class) " + m.name + "(" + parameters(t, m, [], bound) + "): Result[" + fern_type(kind, bound) + ", jni.JavaError] {\n";
  out += call_start(kind) + entry + ", " + target(m) + ", c.id_" + m.name + ", 0 as usize);\n";
  return out + ending(kind, bound);
}

function member_function(c: classfile.Class, m: Bound, bound: string[]): Result[string, string] {
  if (m.is_field) {
    let Some(kind) = classfile.field_type(m.descriptor) else {
      return Err(c.name + "." + m.java + " has a descriptor that is not a type");
    };
    return Ok(field_function(c, m, kind, bound));
  }
  let Some(sig) = classfile.signature(m.descriptor) else {
    return Err(c.name + "." + m.java + " has a descriptor that is not a signature");
  };
  return Ok(method_function(c, m, sig, bound));
}
```

### `pub function generate`

```fern
<<pub function generate>>=
// The names of the classes of the requests. Two classes with one simple
// name are an error.
function bound_classes(requests: Request[]): Result[string[], string] {
  let out: string[] = [];
  let types: string[] = [];
  for r in requests {
    if (has(types, type_name(r.class))) {
      return Err("two requests have a class with the name " + type_name(r.class));
    }
    types = types.append(type_name(r.class));
    out = out.append(r.class);
  }
  return Ok(out);
}

// The part of the module for the class of the request `r`.
function class_text(classes: classfile.Class[], r: Request, bound: string[]): Result[string, string] {
  let Some(c) = find(classes, r.class) else {
    return Err("the classes do not have " + r.class);
  };
  let lost = missing(c, r);
  if (lost.len() > 0) {
    return Err(r.class + " has no public member " + lost);
  }
  let fields = fields_of(c, r);
  let members = bound_members(methods_of(c, r), fields);
  let out = object_struct(classes, c, bound);
  for f in fields {
    if (is_constant(f)) {
      out += constant_line(type_name(c.name), f);
    }
  }
  out += class_struct(c, members);
  for m in members {
    out += member_function(c, m, bound)?;
  }
  return Ok(out);
}

// The text of a Fern module with the wrappers of `requests`. `classes`
// holds the class of each request, and the parents that conversions are
// for.
pub function generate(classes: classfile.Class[], requests: Request[]): Result[string, string] {
  let bound = bound_classes(requests)?;
  let out = "// Typed JNI bindings. std/jni/bind wrote this module. Do not edit it.\nimport \"std/jni\";\n\n";
  for r in requests {
    out += class_text(classes, r, bound)?;
  }
  // Each part ends with an empty line, and a module ends with none.
  return Ok(slice_unchecked(out, 0, out.len() - 1) + "");
}
```
