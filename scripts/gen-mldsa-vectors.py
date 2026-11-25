#!/usr/bin/env python3
# gen-mldsa-vectors.py: writes the ML-DSA-65 vector suites under tests/stdlib.
#
#   gen-mldsa-vectors.py WYCHEPROOF-DIR ACVP-DIR
#
# WYCHEPROOF-DIR holds the mldsa_65_*_test.json files of testvectors_v1 of
# https://github.com/C2SP/wycheproof. ACVP-DIR holds the ML-DSA-keyGen-FIPS204,
# ML-DSA-sigGen-FIPS204 and ML-DSA-sigVer-FIPS204 directories of gen-val/json-files
# of https://github.com/usnistgov/ACVP-Server. The suites keep the SHA3-256 of
# each expected key and signature, not the value itself.
import hashlib
import json
import os
import sys

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "tests", "stdlib")

HELPERS = """
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

function zeros(n: i32): u8[] {
  let b: u8[] = __alloc_u8(n);
  let i: i32 = 0;
  while (i < n) {
    b = b.with(i, 0 as u8);
    i += 1;
  }
  return b;
}

// mu of the internal interface on a message: H(tr || msg), with tr = H(pk).
function internal_mu(tr: [u8], msg: [u8]): u8[] {
  return crypto.shake256_new().update(tr).update(msg).reader().squeeze(64).0;
}

// Mode 0 signs a message under a context. Mode 1 signs mu. Mode 2 signs a
// message through the internal interface.
function signed(sk: [u8], m: [u8], ctx: [u8], rnd: [u8], mode: i32): Result[u8[], mldsa.MlDsaError] {
  if (mode == 1) {
    return mldsa.sign_mu_derand(sk, m, rnd);
  }
  if (mode == 2) {
    return mldsa.sign_mu_derand(sk, internal_mu(sk[64:128], m), rnd);
  }
  return mldsa.sign_derand(sk, m, ctx, rnd);
}

function check_sign(sk: [u8], m: string, ctx: string, rnd: string, mode: i32, sig: string, want: i32, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  match (signed(sk, bytes(m), bytes(ctx), bytes(rnd), mode)) {
    Ok(s) => {
      if (want == 0) {
        return tc + "invalid input signed";
      }
      if (digest(s) != sig) {
        return tc + "signature differs";
      }
    },
    Err(e) => {
      if (want == 1) {
        return tc + "refused: " + e.message();
      }
    }
  }
  return "";
}

function check_seed_sign(seed: string, pk: string, m: string, ctx: string, rnd: string, mode: i32, sig: string, want: i32, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  let Ok(k) = mldsa.keygen_derand(bytes(seed)) else {
    if (want == 1) {
      return tc + "seed refused";
    }
    return "";
  };
  if (hex.encode(k.public_key) != pk) {
    return tc + "public key differs";
  }
  return check_sign(k.private_key, m, ctx, rnd, mode, sig, want, id);
}

function check_verify(pk: [u8], m: string, ctx: string, sig: string, mode: i32, want: i32, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  let ok: boolean = false;
  if (mode == 1) {
    ok = mldsa.verify_mu(pk, bytes(m), bytes(sig));
  } else if (mode == 2) {
    ok = mldsa.verify_mu(pk, internal_mu(crypto.shake256_new().update(pk).reader().squeeze(64).0, bytes(m)), bytes(sig));
  } else {
    ok = mldsa.verify(pk, bytes(m), bytes(ctx), bytes(sig));
  }
  if (want == 1 && !ok) {
    return tc + "valid signature refused";
  }
  if (want == 0 && ok) {
    return tc + "invalid signature accepted";
  }
  return "";
}

function check_keygen(seed: string, pk: string, sk: string, id: i32): string {
  let tc: string = "tc " + id.to_string() + ": ";
  let Ok(k) = mldsa.keygen_derand(bytes(seed)) else {
    return tc + "seed refused";
  };
  if (digest(k.public_key) != pk) {
    return tc + "public key differs";
  }
  if (digest(k.private_key) != sk) {
    return tc + "private key differs";
  }
  return "";
}
"""


def s(x):
    return '"' + x + '"'


def h(x):
    return hashlib.sha3_256(bytes.fromhex(x)).hexdigest()


ZERO_RND = "00" * 32


def write(name, header, groups, title):
    lines = [header.rstrip("\n"), HELPERS.rstrip("\n"), ""]
    its = []
    for gi, (label, lets, body) in enumerate(groups):
        fn = "test_group_" + str(gi + 1)
        lines.append("function " + fn + "(): test.TestOutcome {")
        for let in lets:
            lines.append("  " + let)
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
    print(path, len(groups), "groups")


def want(result):
    return {"valid": 1, "invalid": 0}[result]


def message(t):
    # A vector with mu and no message is for the internal interface.
    if "msg" in t:
        return t["msg"], 0
    return t["mu"], 1


def wycheproof_sign(d):
    groups = []
    for f, seeded in (("mldsa_65_sign_seed_test.json", True), ("mldsa_65_sign_noseed_test.json", False)):
        data = json.load(open(os.path.join(d, f)))
        for g in data["testGroups"]:
            body = []
            if seeded:
                lets = []
            else:
                lets = ["let sk: u8[] = bytes(" + s(g["privateKey"]) + ");"]
            for t in g["tests"]:
                m, mode = message(t)
                sig = h(t["sig"]) if t["sig"] else ""
                args = "%s, %s, %s, %d, %s, %d, %d" % (s(m), s(t.get("ctx", "")), s(t.get("rnd", ZERO_RND)), mode, s(sig), want(t["result"]), t["tcId"])
                if seeded:
                    body.append("check_seed_sign(%s, %s, %s)" % (s(g["privateSeed"]), s(g.get("publicKey") or ""), args))
                else:
                    body.append("check_sign(sk, %s)" % args)
            first = g["tests"][0]["tcId"]
            groups.append(("%s tc %d" % ("seed" if seeded else "expanded key", first), lets, body))
    header = """// Generated by scripts/gen-mldsa-vectors.py from Wycheproof
// testvectors_v1/mldsa_65_sign_seed_test.json and
// testvectors_v1/mldsa_65_sign_noseed_test.json. Do not edit by hand.
"""
    write("wycheproof_mldsa65_sign_test.fern", header, groups, "wycheproof ml-dsa-65 sign")


def wycheproof_verify(d):
    data = json.load(open(os.path.join(d, "mldsa_65_verify_test.json")))
    groups = []
    for g in data["testGroups"]:
        tests = g["tests"]
        for i in range(0, len(tests), 16):
            chunk = tests[i:i + 16]
            lets = ["let pk: u8[] = bytes(" + s(g["publicKey"]) + ");"]
            body = []
            for t in chunk:
                body.append("check_verify(pk, %s, %s, %s, 0, %d, %d)" % (s(t["msg"]), s(t.get("ctx", "")), s(t["sig"]), want(t["result"]), t["tcId"]))
            groups.append(("vectors %d to %d" % (chunk[0]["tcId"], chunk[-1]["tcId"]), lets, body))
    header = """// Generated by scripts/gen-mldsa-vectors.py from Wycheproof
// testvectors_v1/mldsa_65_verify_test.json. Do not edit by hand.
"""
    write("wycheproof_mldsa65_verify_test.fern", header, groups, "wycheproof ml-dsa-65 verify")


def acvp_groups(d, name):
    data = json.load(open(os.path.join(d, name, "internalProjection.json")))
    return [g for g in data["testGroups"] if g["parameterSet"] == "ML-DSA-65"]


MODES = ("pure", "external mu", "internal")


def acvp_mode(g):
    if g["signatureInterface"] == "external":
        return 0
    if g["externalMu"]:
        return 1
    return 2


def acvp(d):
    groups = []
    for g in acvp_groups(d, "ML-DSA-keyGen-FIPS204"):
        body = ["check_keygen(%s, %s, %s, %d)" % (s(t["seed"]), s(h(t["pk"])), s(h(t["sk"])), t["tcId"]) for t in g["tests"]]
        groups.append(("keyGen group %d" % g["tgId"], [], body))
    # HashML-DSA (preHash) is not in the module.
    for g in acvp_groups(d, "ML-DSA-sigGen-FIPS204"):
        if g["preHash"] == "preHash":
            continue
        mode = acvp_mode(g)
        body = []
        for t in g["tests"]:
            m = t["mu"] if mode == 1 else t["message"]
            rnd = ZERO_RND if g["deterministic"] else t["rnd"]
            body.append("check_sign(bytes(%s), %s, %s, %s, %d, %s, 1, %d)" % (s(t["sk"]), s(m), s(t.get("context", "")), s(rnd), mode, s(h(t["signature"])), t["tcId"]))
        label = "sigGen group %d (%s, %s)" % (g["tgId"], MODES[mode], "deterministic" if g["deterministic"] else "hedged")
        groups.append((label, [], body))
    for g in acvp_groups(d, "ML-DSA-sigVer-FIPS204"):
        if g["preHash"] == "preHash":
            continue
        mode = acvp_mode(g)
        body = []
        for t in g["tests"]:
            m = t["mu"] if mode == 1 else t["message"]
            body.append("check_verify(bytes(%s), %s, %s, %s, %d, %d, %d)" % (s(t["pk"]), s(m), s(t.get("context", "")), s(t["signature"]), mode, 1 if t["testPassed"] else 0, t["tcId"]))
        groups.append(("sigVer group %d (%s)" % (g["tgId"], MODES[mode]), [], body))
    header = """// Generated by scripts/gen-mldsa-vectors.py from the ML-DSA-65 groups of the
// NIST ACVP-Server gen-val/json-files ML-DSA-keyGen-FIPS204,
// ML-DSA-sigGen-FIPS204 and ML-DSA-sigVer-FIPS204. The HashML-DSA groups are
// left out. Do not edit by hand.
"""
    write("mldsa_acvp_test.fern", header, groups, "acvp ml-dsa-65")


def main():
    if len(sys.argv) != 3:
        print("usage: gen-mldsa-vectors.py WYCHEPROOF-DIR ACVP-DIR", file=sys.stderr)
        sys.exit(2)
    wycheproof_sign(sys.argv[1])
    wycheproof_verify(sys.argv[1])
    acvp(sys.argv[2])


if __name__ == "__main__":
    main()
