# Email address text

`is_email_like` checks for a nonempty local part before the first `@` and a dot somewhere after it. Use this for a quick shape check. It accepts some strings that cannot be used as mailboxes.

## Executable example

```fern test name=std-email-example
import "std/email";

function main(): i32 {
  if (!"alice@example.org".is_email_like()) { return 1; }
  if ("@example.org".is_email_like() || "alice@localhost".is_email_like()) { return 2; }
  return 0;
}
```

## Implementation

```fern
<<*>>=
// This module recognizes the basic shape of an email address.

import "std/string";

pub function (s: string) is_email_like(): boolean {
  match (s.split_once("@")) {
    Some(p) => {
      let lo = p.0;
      let dom = p.1;
      if (lo.len() == 0) {
        return false;
      }
      if (dom.len() == 0) {
        return false;
      }
      return dom.contains(".");
    },
    None => {
      return false;
    }
  }
}
```
