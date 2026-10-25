# Command-line specifications

This module parses command-line arguments.

## Model

`CliSpec` describes options, flags, and subcommands. `parse` returns option values, operands, the selected command, and a parse error.

## Contracts

A value option consumes an argument. A flag records presence. `--` ends option interpretation so later arguments remain operands.

## Executable example

```fern test name=std-cli-example
import "std/cli";

function main(): i32 {
  let spec = cli.new("greet", "Print a name").option("name", "n", "Name");
  let parsed = spec.parse(["greet", "--name", "Ada"]);
  if (parsed.is_error() || parsed.value_or("name", "") != "Ada") { return 1; }
  return 0;
}
```

## Module assembly

The root chunk preserves the declaration order. The chunks below contain the complete implementation.

```fern
<<*>>=
<<dependencies 1>>

<<2 struct CliFlag>>

<<3 pub function new(name: string, about: string): CliSpec>>

<<4 function __cli_find_long(s: CliSpec, name: string): Option[CliFlag]>>

<<5 function __cli_parse(s: CliSpec, argv: string[], start: i32, opts: Map[string, string]): CliArgs>>

<<6 pub function (s: CliSpec) parse(argv: string[]): CliArgs>>

<<7 function __cli_usage_flags(flags: CliFlag[], indent: string): string>>

<<8 pub function (s: CliSpec) usage(): string>>

<<9 function __cli_roff(s: string): string>>

<<10 pub function (s: CliSpec) manpage(section: string): string>>

<<11 function __cli_ident(name: string): string>>

<<12 function __cli_completion_fish(s: CliSpec): string>>

<<13 pub function (s: CliSpec) completion(shell: string): Option[string]>>

<<14 function __cli_sgr(code: string, s: string): string>>

<<15 pub function red(s: string): string>>

<<16 function __cli_kdl(s: string): string>>

<<17 pub function (s: CliSpec) usage_spec(): string>>
```

### `dependencies 1`

```fern
<<dependencies 1>>=
// This module parses command-line arguments.
import "std/string";
import "std/utf8" as utf8;
import "core/map";
```

### `2 struct CliFlag`

```fern
<<2 struct CliFlag>>=
struct CliFlag { long: string, short: string, takes_value: boolean, help: string }

pub struct CliSpec { name: string, about: string, flags: CliFlag[], commands: CliSpec[] }

pub struct CliArgs { opts: Map[string, string], positionals: string[], command: string, error: string }

struct CliStep { opts: Map[string, string], next: i32, error: string }
```

### `3 pub function new(name: string, about: string): CliSpec`

```fern
<<3 pub function new(name: string, about: string): CliSpec>>=
pub function new(name: string, about: string): CliSpec {
  let fs: CliFlag[] = [];
  let cs: CliSpec[] = [];
  return CliSpec { name: name, about: about, flags: fs, commands: cs };
}

pub function (s: CliSpec) option(long: string, short: string, help: string): CliSpec {
  return CliSpec { ...s, flags: s.flags.append(CliFlag { long: long, short: short, takes_value: true, help: help }) };
}

pub function (s: CliSpec) flag(long: string, short: string, help: string): CliSpec {
  return CliSpec { ...s, flags: s.flags.append(CliFlag { long: long, short: short, takes_value: false, help: help }) };
}

pub function (s: CliSpec) command(sub: CliSpec): CliSpec {
  return CliSpec { ...s, commands: s.commands.append(sub) };
}
```

### `4 function __cli_find_long(s: CliSpec, name: string): Option[CliFlag]`

```fern
<<4 function __cli_find_long(s: CliSpec, name: string): Option[CliFlag]>>=
function __cli_find_long(s: CliSpec, name: string): Option[CliFlag] {
  let i: i32 = 0;
  while (i < s.flags.len()) {
    if (s.flags[i].long == name) {
      return Some(s.flags[i]);
    }
    i = i + 1;
  }
  return None;
}

function __cli_find_short_byte(s: CliSpec, b: i32): Option[CliFlag] {
  let i: i32 = 0;
  while (i < s.flags.len()) {
    if (s.flags[i].short.len() == 1 && s.flags[i].short[0] as i32 == b) {
      return Some(s.flags[i]);
    }
    i = i + 1;
  }
  return None;
}

function __cli_find_command(s: CliSpec, name: string): Option[CliSpec] {
  let i: i32 = 0;
  while (i < s.commands.len()) {
    if (s.commands[i].name == name) {
      return Some(s.commands[i]);
    }
    i = i + 1;
  }
  return None;
}

function __cli_short_name_at(cluster: string, j: i32): string {
  let b: i32 = cluster[j] as i32;
  if (b < 128) {
    return string_from_bytes_unchecked([cluster[j]]);
  }
  match (utf8.decode_at(cluster, j)) {
    Some(pair) => {
      return slice_unchecked(cluster, j, j + pair.1) + "";
    },
    None => {
      return "�";
    }
  }
}

function __cli_err(positionals: string[], msg: string): CliArgs {
  let empty: Map[string, string] = map_new(1);
  return CliArgs { opts: empty, positionals: positionals, command: "", error: msg };
}

function __cli_step_err(opts: Map[string, string], i: i32, msg: string): CliStep {
  return CliStep { opts: opts, next: i, error: msg };
}

function __cli_long(s: CliSpec, opts: Map[string, string], argv: string[], i: i32): CliStep {
  let a: string = argv[i];
  let body: str = slice_unchecked(a, 2, a.len());
  let name: str = body;
  let inline_val: string = "";
  let inline: boolean = false;
  if let Some(eq) = body.index_of("=") {
    name = slice_unchecked(body, 0, eq);
    inline_val = slice_unchecked(body, eq + 1, body.len()) + "";
    inline = true;
  }
  match (__cli_find_long(s, name)) {
    Some(f) => {
      if (!f.takes_value) {
        if (inline) {
          return __cli_step_err(opts, i, "flag --" + name + " takes no value");
        }
        return CliStep { opts: opts.insert(f.long, "true"), next: i + 1, error: "" };
      }
      if (inline) {
        return CliStep { opts: opts.insert(f.long, inline_val), next: i + 1, error: "" };
      }
      if (i + 1 < argv.len()) {
        return CliStep { opts: opts.insert(f.long, argv[i + 1]), next: i + 2, error: "" };
      }
      return __cli_step_err(opts, i, "option --" + name + " requires a value");
    },
    None => {
      return __cli_step_err(opts, i, "unknown option --" + name);
    }
  }
}

function __cli_cluster(s: CliSpec, opts: Map[string, string], argv: string[], i: i32): CliStep {
  let a: string = argv[i];
  let cluster: str = slice_unchecked(a, 1, a.len());
  let j: i32 = 0;
  while (j < cluster.len()) {
    match (__cli_find_short_byte(s, cluster[j] as i32)) {
      Some(f) => {
        if (!f.takes_value) {
          opts = opts.insert(f.long, "true");
          j = j + 1;
        } else {
          let glued: str = slice_unchecked(cluster, j + 1, cluster.len());
          if (glued.len() > 0) {
            return CliStep { opts: opts.insert(f.long, glued + ""), next: i + 1, error: "" };
          }
          if (i + 1 < argv.len()) {
            return CliStep { opts: opts.insert(f.long, argv[i + 1]), next: i + 2, error: "" };
          }
          return __cli_step_err(opts, i, "option -" + f.short + " requires a value");
        }
      },
      None => {
        return __cli_step_err(opts, i, "unknown option -" + __cli_short_name_at(cluster, j));
      }
    }
  }
  return CliStep { opts: opts, next: i + 1, error: "" };
}
```

### `5 function __cli_parse(s: CliSpec, argv: string[], start: i32, opts: Map[string, string]): CliArgs`

```fern
<<5 function __cli_parse(s: CliSpec, argv: string[], start: i32, opts: Map[string, string]): CliArgs>>=
function __cli_parse(s: CliSpec, argv: string[], start: i32, opts: Map[string, string]): CliArgs {
  let positionals: string[] = [];
  let i: i32 = start;
  let no_more: boolean = false;
  while (i < argv.len()) {
    let a: string = argv[i];
    if (no_more) {
      positionals = positionals.append(a);
      i = i + 1;
    } else if (a == "--") {
      no_more = true;
      i = i + 1;
    } else if (a.starts_with("--") || a.len() >= 2 && a[0] == 45) {
      let st: CliStep = __cli_long(s, opts, argv, i);
      if (!a.starts_with("--")) {
        st = __cli_cluster(s, opts, argv, i);
      }
      if (st.error.len() > 0) {
        return __cli_err(positionals, st.error);
      }
      opts = st.opts;
      i = st.next;
    } else if (s.commands.len() > 0 && positionals.len() == 0) {
      match (__cli_find_command(s, a)) {
        Some(sub) => {
          let r: CliArgs = __cli_parse(sub, argv, i + 1, opts);
          if (r.error.len() > 0) {
            return r;
          }
          return CliArgs { ...r, command: sub.name };
        },
        None => {
          return __cli_err(positionals, "unknown command " + a);
        }
      }
    } else {
      positionals = positionals.append(a);
      i = i + 1;
    }
  }
  return CliArgs { opts: opts, positionals: positionals, command: "", error: "" };
}
```

### `6 pub function (s: CliSpec) parse(argv: string[]): CliArgs`

```fern
<<6 pub function (s: CliSpec) parse(argv: string[]): CliArgs>>=
pub function (s: CliSpec) parse(argv: string[]): CliArgs {
  let opts: Map[string, string] = map_new(8);
  return __cli_parse(s, argv, 1, opts);
}

pub function (a: CliArgs) is_error(): boolean {
  return a.error.len() > 0;
}

pub function (a: CliArgs) error_text(): string {
  return a.error;
}

pub function (a: CliArgs) value(name: string): Option[string] {
  return a.opts.get(name);
}

pub function (a: CliArgs) value_or(name: string, fallback: string): string {
  match (a.opts.get(name)) {
    Some(v) => {
      return v;
    },
    None => {
      return fallback;
    }
  }
}

pub function (a: CliArgs) is_set(name: string): boolean {
  return a.opts.contains(name);
}

pub function (a: CliArgs) operands(): string[] {
  return a.positionals;
}

pub function (a: CliArgs) operand(idx: i32): Option[string] {
  if (idx < 0 || idx >= a.positionals.len()) {
    return None;
  }
  return Some(a.positionals[idx]);
}

pub function (a: CliArgs) subcommand(): Option[string] {
  if (a.command.len() == 0) {
    return None;
  }
  return Some(a.command);
}
```

### `7 function __cli_usage_flags(flags: CliFlag[], indent: string): string`

```fern
<<7 function __cli_usage_flags(flags: CliFlag[], indent: string): string>>=
function __cli_usage_flags(flags: CliFlag[], indent: string): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < flags.len()) {
    let f: CliFlag = flags[i];
    let line: string = indent;
    if (f.short.len() > 0) {
      line = line + "-" + f.short + ", ";
    } else {
      line = line + "    ";
    }
    line = line + "--" + f.long;
    if (f.takes_value) {
      line = line + " <value>";
    }
    out = out + line + "\n";
    if (f.help.len() > 0) {
      out = out + indent + "    " + f.help + "\n";
    }
    i = i + 1;
  }
  return out;
}
```

### `8 pub function (s: CliSpec) usage(): string`

```fern
<<8 pub function (s: CliSpec) usage(): string>>=
pub function (s: CliSpec) usage(): string {
  let shape: string = " [options] [args]\n";
  if (s.commands.len() > 0) {
    shape = " [options] <command> [args]\n";
  }
  let out: string = "Usage: " + s.name + shape;
  if (s.about.len() > 0) {
    out = out + "\n" + s.about + "\n";
  }
  if (s.flags.len() > 0) {
    out = out + "\nOptions:\n" + __cli_usage_flags(s.flags, "  ");
  }
  if (s.commands.len() > 0) {
    out = out + "\nCommands:\n";
    let i: i32 = 0;
    while (i < s.commands.len()) {
      out = out + "  " + s.commands[i].name;
      if (s.commands[i].about.len() > 0) {
        out = out + "\n      " + s.commands[i].about;
      }
      out = out + "\n";
      i = i + 1;
    }
  }
  return out;
}
```

### `9 function __cli_roff(s: string): string`

```fern
<<9 function __cli_roff(s: string): string>>=
function __cli_roff(s: string): string {
  let out: string = s.replace("\\", "\\\\");
  if (out.len() > 0 && (out[0] as i32 == 46 || out[0] as i32 == 39)) {
    out = "\\&" + out;
  }
  return out;
}

function __cli_man_flags(flags: CliFlag[]): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < flags.len()) {
    let f: CliFlag = flags[i];
    out = out + ".TP\n";
    let tag: string = "";
    if (f.short.len() > 0) {
      tag = "\\fB\\-" + __cli_roff(f.short) + "\\fR, ";
    }
    tag = tag + "\\fB\\-\\-" + __cli_roff(f.long) + "\\fR";
    if (f.takes_value) {
      tag = tag + " \\fI<value>\\fR";
    }
    out = out + tag + "\n";
    if (f.help.len() > 0) {
      out = out + __cli_roff(f.help) + "\n";
    }
    i = i + 1;
  }
  return out;
}
```

### `10 pub function (s: CliSpec) manpage(section: string): string`

```fern
<<10 pub function (s: CliSpec) manpage(section: string): string>>=
pub function (s: CliSpec) manpage(section: string): string {
  let out: string = ".TH \"" + __cli_roff(s.name) + "\" \"" + __cli_roff(section) + "\"\n";
  out = out + ".SH NAME\n" + __cli_roff(s.name);
  if (s.about.len() > 0) {
    out = out + " \\- " + __cli_roff(s.about);
  }
  out = out + "\n";
  let shape: string = " [options] [args]";
  if (s.commands.len() > 0) {
    shape = " [options] <command> [args]";
  }
  out = out + ".SH SYNOPSIS\n\\fB" + __cli_roff(s.name) + "\\fR" + shape + "\n";
  if (s.about.len() > 0) {
    out = out + ".SH DESCRIPTION\n" + __cli_roff(s.about) + "\n";
  }
  if (s.flags.len() > 0) {
    out = out + ".SH OPTIONS\n" + __cli_man_flags(s.flags);
  }
  if (s.commands.len() > 0) {
    out = out + ".SH COMMANDS\n";
    let i: i32 = 0;
    while (i < s.commands.len()) {
      let c: CliSpec = s.commands[i];
      out = out + ".TP\n\\fB" + __cli_roff(c.name) + "\\fR\n";
      if (c.about.len() > 0) {
        out = out + __cli_roff(c.about) + "\n";
      }
      i = i + 1;
    }
  }
  return out;
}
```

### `11 function __cli_ident(name: string): string`

```fern
<<11 function __cli_ident(name: string): string>>=
function __cli_ident(name: string): string {
  return name.replace("-", "_").replace(".", "_");
}

function __cli_sq(s: string): string {
  return "'" + s.replace("'", "'\\''") + "'";
}

function __cli_zsh_desc(s: string): string {
  return s.replace("\\", "\\\\").replace("]", "\\]").replace(":", "\\:").replace("'", "'\"'\"'");
}

function __cli_words(s: CliSpec): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < s.flags.len()) {
    out = out + " --" + s.flags[i].long;
    if (s.flags[i].short.len() > 0) {
      out = out + " -" + s.flags[i].short;
    }
    i = i + 1;
  }
  i = 0;
  while (i < s.commands.len()) {
    out = out + " " + s.commands[i].name;
    i = i + 1;
  }
  if (out.len() == 0) {
    return "";
  }
  return slice_unchecked(out, 1, out.len()) + "";
}

function __cli_completion_bash(s: CliSpec): string {
  let fn: string = "_" + __cli_ident(s.name);
  let out: string = fn + "() {\n";
  out = out + "    local cur=\"${COMP_WORDS[COMP_CWORD]}\" words=" + __cli_sq(__cli_words(s)) + "\n";
  if (s.commands.len() > 0) {
    out = out + "    local i\n";
    out = out + "    for ((i = 1; i < COMP_CWORD; i++)); do\n";
    out = out + "        case \"${COMP_WORDS[i]}\" in\n";
    let i: i32 = 0;
    while (i < s.commands.len()) {
      let c: CliSpec = s.commands[i];
      out = out + "            " + c.name + ") words=" + __cli_sq(__cli_words(c)) + " ;;\n";
      i = i + 1;
    }
    out = out + "        esac\n";
    out = out + "    done\n";
  }
  out = out + "    COMPREPLY=($(compgen -W \"$words\" -- \"$cur\"))\n";
  out = out + "}\n";
  out = out + "complete -o default -F " + fn + " " + s.name + "\n";
  return out;
}

function __cli_zsh_opts(s: CliSpec, indent: string): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < s.flags.len()) {
    let f: CliFlag = s.flags[i];
    let tail: string = "[" + __cli_zsh_desc(f.help) + "]";
    if (f.takes_value) {
      tail = tail + ":value:_files";
    }
    if (f.short.len() > 0) {
      out = out + indent + "'(-" + f.short + " --" + f.long + ")'{-" + f.short + ",--" + f.long + "}'" + tail + "'\n";
    } else {
      out = out + indent + "'--" + f.long + tail + "'\n";
    }
    i = i + 1;
  }
  return out;
}

function __cli_completion_zsh(s: CliSpec): string {
  let fn: string = "_" + __cli_ident(s.name);
  let out: string = "#compdef " + s.name + "\n";
  out = out + fn + "() {\n";
  out = out + "    local -a opts cmds\n";
  out = out + "    opts=(\n" + __cli_zsh_opts(s, "        ") + "    )\n";
  if (s.commands.len() == 0) {
    out = out + "    _arguments -s \"${opts[@]}\" '*::arg:_files'\n";
  } else {
    out = out + "    local context state line\n";
    out = out + "    _arguments -s -C \"${opts[@]}\" '1:command:->cmd' '*::arg:->args'\n";
    out = out + "    case \"$state\" in\n";
    out = out + "        cmd)\n            cmds=(\n";
    let i: i32 = 0;
    while (i < s.commands.len()) {
      let c: CliSpec = s.commands[i];
      out = out + "                '" + c.name + ":" + __cli_zsh_desc(c.about) + "'\n";
      i = i + 1;
    }
    out = out + "            )\n            _describe 'command' cmds ;;\n";
    out = out + "        args)\n            case \"$line[1]\" in\n";
    i = 0;
    while (i < s.commands.len()) {
      let c: CliSpec = s.commands[i];
      out = out + "                " + c.name + ")\n";
      out = out + "                    opts=(\n" + __cli_zsh_opts(c, "                        ") + "                    )\n";
      out = out + "                    _arguments -s \"${opts[@]}\" '*::arg:_files' ;;\n";
      i = i + 1;
    }
    out = out + "            esac ;;\n";
    out = out + "    esac\n";
  }
  out = out + "}\n";
  out = out + fn + " \"$@\"\n";
  return out;
}

function __cli_fish_flags(prog: string, s: CliSpec, cond: string): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < s.flags.len()) {
    let f: CliFlag = s.flags[i];
    let line: string = "complete -c " + prog + cond + " -l " + f.long;
    if (f.short.len() > 0) {
      line = line + " -s " + f.short;
    }
    if (f.help.len() > 0) {
      line = line + " -d " + __cli_sq(f.help);
    }
    if (f.takes_value) {
      line = line + " -r";
    }
    out = out + line + "\n";
    i = i + 1;
  }
  return out;
}
```

### `12 function __cli_completion_fish(s: CliSpec): string`

```fern
<<12 function __cli_completion_fish(s: CliSpec): string>>=
function __cli_completion_fish(s: CliSpec): string {
  if (s.commands.len() == 0) {
    return __cli_fish_flags(s.name, s, "");
  }
  let out: string = __cli_fish_flags(s.name, s, " -n __fish_use_subcommand");
  let i: i32 = 0;
  while (i < s.commands.len()) {
    let c: CliSpec = s.commands[i];
    let line: string = "complete -c " + s.name + " -n __fish_use_subcommand -a " + c.name;
    if (c.about.len() > 0) {
      line = line + " -d " + __cli_sq(c.about);
    }
    out = out + line + "\n";
    out = out + __cli_fish_flags(s.name, c, " -n " + __cli_sq("__fish_seen_subcommand_from " + c.name));
    i = i + 1;
  }
  return out;
}
```

### `13 pub function (s: CliSpec) completion(shell: string): Option[string]`

```fern
<<13 pub function (s: CliSpec) completion(shell: string): Option[string]>>=
pub function (s: CliSpec) completion(shell: string): Option[string] {
  if (shell == "bash") {
    return Some(__cli_completion_bash(s));
  }
  if (shell == "zsh") {
    return Some(__cli_completion_zsh(s));
  }
  if (shell == "fish") {
    return Some(__cli_completion_fish(s));
  }
  return None;
}

pub function color_enabled(): boolean {
  match (env("FORCE_COLOR")) {
    Some(_) => {
      return true;
    },
    None => {}
  }
  match (env("NO_COLOR")) {
    Some(_) => {
      return false;
    },
    None => {}
  }
  match (env("TERM")) {
    Some(t) => {
      if (t == "dumb") {
        return false;
      }
    },
    None => {}
  }
  return isatty(1);
}
```

### `14 function __cli_sgr(code: string, s: string): string`

```fern
<<14 function __cli_sgr(code: string, s: string): string>>=
function __cli_sgr(code: string, s: string): string {
  if (!color_enabled()) {
    return s;
  }
  return "\x1b[" + code + "m" + s + "\x1b[0m";
}
```

### `15 pub function red(s: string): string`

```fern
<<15 pub function red(s: string): string>>=
pub function red(s: string): string {
  return __cli_sgr("31", s);
}

pub function green(s: string): string {
  return __cli_sgr("32", s);
}

pub function yellow(s: string): string {
  return __cli_sgr("33", s);
}

pub function blue(s: string): string {
  return __cli_sgr("34", s);
}

pub function magenta(s: string): string {
  return __cli_sgr("35", s);
}

pub function cyan(s: string): string {
  return __cli_sgr("36", s);
}

pub function bold(s: string): string {
  return __cli_sgr("1", s);
}

pub function dim(s: string): string {
  return __cli_sgr("2", s);
}
```

### `16 function __cli_kdl(s: string): string`

```fern
<<16 function __cli_kdl(s: string): string>>=
function __cli_kdl(s: string): string {
  return "\"" + s.replace("\\", "\\\\").replace("\"", "\\\"").replace("\n", "\\n") + "\"";
}

function __cli_kdl_flag(f: CliFlag): string {
  let names: string = "";
  if (f.short.len() > 0) {
    names = "-" + f.short + " ";
  }
  names = names + "--" + f.long;
  if (f.takes_value) {
    names = names + " <" + f.long + ">";
  }
  let out: string = "flag " + __cli_kdl(names);
  if (f.help.len() > 0) {
    out = out + " help=" + __cli_kdl(f.help);
  }
  return out;
}

function __cli_kdl_body(s: CliSpec, indent: string): string {
  let out: string = "";
  let i: i32 = 0;
  while (i < s.flags.len()) {
    out = out + indent + __cli_kdl_flag(s.flags[i]) + "\n";
    i = i + 1;
  }
  if (s.commands.len() == 0) {
    return out + indent + "arg \"[args]...\" var=#true\n";
  }
  i = 0;
  while (i < s.commands.len()) {
    let c: CliSpec = s.commands[i];
    out = out + indent + "cmd " + __cli_kdl(c.name);
    if (c.about.len() > 0) {
      out = out + " help=" + __cli_kdl(c.about);
    }
    out = out + " {\n" + __cli_kdl_body(c, indent + "  ") + indent + "}\n";
    i = i + 1;
  }
  return out;
}
```

### `17 pub function (s: CliSpec) usage_spec(): string`

```fern
<<17 pub function (s: CliSpec) usage_spec(): string>>=
pub function (s: CliSpec) usage_spec(): string {
  let out: string = "bin " + __cli_kdl(s.name) + "\n";
  if (s.about.len() > 0) {
    out = out + "about " + __cli_kdl(s.about) + "\n";
  }
  return out + __cli_kdl_body(s, "");
}
```
