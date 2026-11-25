#!/usr/bin/env python3
# gen-mldsa-vectors.py: writes the ML-DSA-65 vector suites under tests/stdlib.
# DIR holds these files:
#   mldsa_65_sign_seed_test.json, mldsa_65_sign_noseed_test.json and
#   mldsa_65_verify_test.json from testvectors_v1 of
#   https://github.com/C2SP/wycheproof
#   ML-DSA-keyGen-FIPS204.json, ML-DSA-sigGen-FIPS204.json and
#   ML-DSA-sigVer-FIPS204.json, the internalProjection.json files of
#   gen-val/json-files in https://github.com/usnistgov/ACVP-Server
# The suites compare each signature with the SHA3-256 of the expected one.
import hashlib
import json
import os
import sys

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "tests", "stdlib")

# The ACVP signature groups take this many cases each, to keep the suite small.
ACVP_SIGN_CASES = 5

PRELUDE = """
import "std/test";
import "std/hex";
import "std/codec";
import "std/i32";
import "std/crypto" as crypto;
import "std/crypto/mldsa";

function bytes(h: string): u8[] {
  match (hex.decode(h)) {
    Ok(b) => {
      return b;
    },
    Err(e) => {
      return [];
    }
  }
  return [];
}

function digest(b: [u8]): string {
  return hex.encode(crypto.sha3_256(b));
}

// A signature, or a refusal when want is empty.
function signed(r: Result[u8[], mldsa.MlDsaError], want: string, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  match (r) {
    Ok(sig) => {
      if (want == "") {
        return tc + "invalid input accepted";
      }
      if (digest(sig) != want) {
        return tc + "signature differs";
      }
    },
    Err(e) => {
      if (want != "") {
        return tc + "refused: " + e.message();
      }
    }
  }
  return "";
}

function verified(ok: boolean, want: boolean, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  if (want && !ok) {
    return tc + "valid signature refused";
  }
  if (!want && ok) {
    return tc + "invalid signature accepted";
  }
  return "";
}

function mu_of(pk: string, msg: string, ctx: string, want: string, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  match (mldsa.external_mu(bytes(pk), bytes(msg), bytes(ctx))) {
    Ok(mu) => {
      if (hex.encode(mu) != want) {
        return tc + "mu differs";
      }
    },
    Err(e) => {
      return tc + "mu refused: " + e.message();
    }
  }
  return "";
}

function keys(seed: string, pk: string, sk: string, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  let Ok(k) = mldsa.keygen_derand(bytes(seed)) else {
    if (pk == "") {
      return "";
    }
    return tc + "seed refused";
  };
  if (pk == "") {
    return tc + "invalid seed accepted";
  }
  if (digest(k.public_key) != pk) {
    return tc + "public key differs";
  }
  if (sk != "" && digest(k.private_key) != sk) {
    return tc + "private key differs";
  }
  return "";
}
"""


def s(x):
    return '"' + x + '"'


def digest(h):
    return hashlib.sha3_256(bytes.fromhex(h)).hexdigest()


def wycheproof_sign(data, seeded):
    groups = []
    for g in data["testGroups"]:
        body = []
        pk = g.get("publicKey") or ""
        if seeded:
            seed = g["privateSeed"]
            body.append("keys(%s, %s, %s, %d)" % (s(seed), s(digest(pk) if pk else ""), s(""), g["tests"][0]["tcId"]))
            sk = "k.private_key"
            if not pk:
                groups.append(("seed of %d bytes" % (len(seed) // 2), body))
                continue
            pre = "let Ok(k) = mldsa.keygen_derand(bytes(%s)) else {\n    return test.fail(\"seed refused\");\n  };" % s(seed)
        else:
            sk = "sk"
            pre = "let sk: u8[] = bytes(%s);" % s(g["privateKey"])
        for t in g["tests"]:
            want = digest(t["sig"]) if t["result"] == "valid" else ""
            rnd = t.get("rnd", "00" * 32)
            if "msg" in t:
                call = "mldsa.sign_derand(%s, bytes(%s), bytes(%s), bytes(%s))" % (sk, s(t["msg"]), s(t.get("ctx", "")), s(rnd))
                if "mu" in t and pk and t["result"] == "valid":
                    body.append("mu_of(%s, %s, %s, %s, %d)" % (s(pk), s(t["msg"]), s(t.get("ctx", "")), s(t["mu"]), t["tcId"]))
            else:
                call = "mldsa.sign_mu(%s, bytes(%s), bytes(%s))" % (sk, s(t["mu"]), s(rnd))
            body.append("signed(%s, %s, %d)" % (call, s(want), t["tcId"]))
        label = "key %s" % ((pk or g.get("privateKey") or "")[:16])
        if pre:
            body = [("PRE", pre)] + body
        groups.append((label, body))
    return groups


def wycheproof_verify(data):
    groups = []
    for g in data["testGroups"]:
        pk = g["publicKey"]
        body = []
        for t in g["tests"]:
            call = "mldsa.verify(pk, bytes(%s), bytes(%s), bytes(%s))" % (s(t["msg"]), s(t.get("ctx", "")), s(t["sig"]))
            body.append("verified(%s, %s, %d)" % (call, "true" if t["result"] == "valid" else "false", t["tcId"]))
        groups.append(("key %s" % pk[:16], [("PRE", "let pk: u8[] = bytes(%s);" % s(pk))] + body))
    return groups


def acvp_groups(path):
    with open(path) as f:
        return [g for g in json.load(f)["testGroups"] if g["parameterSet"] == "ML-DSA-65" and g.get("preHash") != "preHash"]


def acvp_call(g, t, sign):
    rnd = t.get("rnd", "00" * 32)
    if g["externalMu"]:
        if sign:
            return "mldsa.sign_mu(bytes(%s), bytes(%s), bytes(%s))" % (s(t["sk"]), s(t["mu"]), s(rnd))
        return "mldsa.verify_mu(bytes(%s), bytes(%s), bytes(%s))" % (s(t["pk"]), s(t["mu"]), s(t["signature"]))
    if g["signatureInterface"] == "internal":
        if sign:
            return "mldsa.sign_internal(bytes(%s), bytes(%s), bytes(%s))" % (s(t["sk"]), s(t["message"]), s(rnd))
        return "mldsa.verify_internal(bytes(%s), bytes(%s), bytes(%s))" % (s(t["pk"]), s(t["message"]), s(t["signature"]))
    if sign:
        return "mldsa.sign_derand(bytes(%s), bytes(%s), bytes(%s), bytes(%s))" % (s(t["sk"]), s(t["message"]), s(t["context"]), s(rnd))
    return "mldsa.verify(bytes(%s), bytes(%s), bytes(%s), bytes(%s))" % (s(t["pk"]), s(t["message"]), s(t["context"]), s(t["signature"]))


def acvp_label(g):
    form = "external mu" if g["externalMu"] else g["signatureInterface"]
    return form + (", deterministic" if g.get("deterministic") else "")


def acvp(d):
    groups = []
    for g in acvp_groups(os.path.join(d, "ML-DSA-keyGen-FIPS204.json")):
        body = ["keys(%s, %s, %s, %d)" % (s(t["seed"]), s(digest(t["pk"])), s(digest(t["sk"])), t["tcId"]) for t in g["tests"]]
        groups.append(("keyGen", body))
    for g in acvp_groups(os.path.join(d, "ML-DSA-sigGen-FIPS204.json")):
        body = ["signed(%s, %s, %d)" % (acvp_call(g, t, True), s(digest(t["signature"])), t["tcId"]) for t in g["tests"][:ACVP_SIGN_CASES]]
        groups.append(("sigGen " + acvp_label(g), body))
    for g in acvp_groups(os.path.join(d, "ML-DSA-sigVer-FIPS204.json")):
        body = ["verified(%s, %s, %d)" % (acvp_call(g, t, False), "true" if t["testPassed"] else "false", t["tcId"]) for t in g["tests"]]
        groups.append(("sigVer " + acvp_label(g), body))
    return groups


def lower(groups):
    # A ("PRE", stmt) entry is a statement before the checks of its group.
    out = []
    for label, body in groups:
        pre = [b[1] for b in body if isinstance(b, tuple)]
        rest = [b for b in body if not isinstance(b, tuple)]
        out.append((label, rest, pre))
    return out


def suite_with_pre(name, header, groups, title):
    lines = [header.rstrip("\n"), PRELUDE.rstrip("\n"), ""]
    its = []
    for gi, (label, body, pre) in enumerate(lower(groups)):
        fn = "test_group_" + str(gi + 1)
        lines.append("function " + fn + "(): test.TestOutcome {")
        for p in pre:
            lines.append("  " + p)
        lines.append("  let r: string = \"\";")
        for stmt in body:
            lines.append("  r = " + stmt + ";")
            lines.append("  if (r != \"\") {")
            lines.append("    return test.fail(r);")
            lines.append("  }")
        lines.append("  return test.pass();")
        lines.append("}")
        lines.append("")
        its.append((label, fn))
    lines.append("function main(): i32 {")
    lines.append("  let r: test.TestRunner = test.new(" + s(title) + ");")
    for label, fn in its:
        lines.append("  r = r.it(" + s(label) + ", " + fn + ");")
    lines.append("  return r.finish();")
    lines.append("}")
    path = os.path.join(OUT, name)
    with open(path, "w") as f:
        f.write("\n".join(lines) + "\n")
    print(path, len(its), "groups")


def load(d, f):
    with open(os.path.join(d, f)) as fh:
        return json.load(fh)


def main():
    if len(sys.argv) != 2:
        print("usage: gen-mldsa-vectors.py DIR", file=sys.stderr)
        sys.exit(2)
    d = sys.argv[1]
    seed = load(d, "mldsa_65_sign_seed_test.json")
    noseed = load(d, "mldsa_65_sign_noseed_test.json")
    verify = load(d, "mldsa_65_verify_test.json")
    n = seed["numberOfTests"] + noseed["numberOfTests"] + verify["numberOfTests"]
    header = ("// Generated by scripts/gen-mldsa-vectors.py from Wycheproof testvectors_v1\n"
              "// mldsa_65_sign_seed_test.json, mldsa_65_sign_noseed_test.json and\n"
              "// mldsa_65_verify_test.json (%d vectors). Do not edit by hand.\n" % n)
    groups = wycheproof_sign(seed, True) + wycheproof_sign(noseed, False) + wycheproof_verify(verify)
    suite_with_pre("wycheproof_mldsa65_test.fern", header, groups, "wycheproof ml-dsa-65")
    header = ("// Generated by scripts/gen-mldsa-vectors.py from the ACVP-Server internalProjection.json\n"
              "// files of ML-DSA-keyGen-FIPS204, ML-DSA-sigGen-FIPS204 and ML-DSA-sigVer-FIPS204,\n"
              "// for ML-DSA-65 with no pre-hash. Do not edit by hand.\n")
    suite_with_pre("acvp_mldsa65_test.fern", header, acvp(d), "acvp ml-dsa-65")


if __name__ == "__main__":
    main()
