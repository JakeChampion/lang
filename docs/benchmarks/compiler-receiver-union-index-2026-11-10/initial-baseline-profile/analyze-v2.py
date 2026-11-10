from pathlib import Path
import bisect, collections, hashlib, json, re

root = Path('/workspace/lang-checker-union-index-20261110-1540/build/baseline-profile-20261110-1540')
result = json.loads((root / 'cold-result.json').read_text())
proof = json.loads((root / 'qualified-symbols.json').read_text())
assert result.get('finished') and result['exit_code'] == 0
assert hashlib.sha256((root / 'symbol-map.stdout').read_bytes()).hexdigest() == proof['symbol_map_sha256']
assert proof['normal_sha256'] == proof['reconstruction_sha256'] == result['normal_sha256']
regions = {}
for row in (root / 'symbol-map.stdout').read_text().splitlines():
    name, start, size = row.split('\t')
    start, size = int(start), int(size)
    if start not in regions or size > regions[start][1]:
        regions[start] = (name, size)
starts = sorted(regions)

def symbol(address):
    i = bisect.bisect_right(starts, address) - 1
    if i >= 0:
        start = starts[i]
        name, size = regions[start]
        if start <= address < start + size:
            return name
    return None

raw = (root / 'cold.callgrind').read_text()
events = re.search(r'^events: (.+)$', raw, re.M)[1].split()
positions = re.search(r'^positions: (.+)$', raw, re.M)[1].split()
assert events == ['Ir']
definitions = {}
addresses = {}
contexts = {}
unresolved = {}
definition = re.compile(r'^(?:c)?fn=\((\d+)\)(?: (.*))?$')
lines = raw.splitlines()
for line in lines:
    match = definition.fullmatch(line)
    if match and match[2] is not None:
        ident = int(match[1])
        address = re.fullmatch(r"0x([0-9a-fA-F]+)('[0-9]+)?", match[2])
        if address:
            pc = int(address[1], 16)
            addresses[ident] = pc
            if address[2]:
                contexts[ident] = address[2]
            name = symbol(pc)
            if name is None:
                unresolved[ident] = match[2]
            definitions[ident] = name or match[2]
        else:
            assert not match[2].startswith('0x'), ('Unparsed address label', match[2])
            definitions[ident] = match[2]

def named(match):
    ident = int(match[2])
    pc = int(match[3], 16)
    name = definitions[ident]
    return match[1] + '(' + str(ident) + ') ' + name + (match[4] or '') + '@0x' + format(pc, 'x')

named_raw = re.sub(r"^((?:c)?fn=)\((\d+)\) 0x([0-9a-fA-F]+)('[0-9]+)?$", named, raw, flags=re.M)
(root / 'cold-symbolized.callgrind').write_text(named_raw)
self_cost = collections.Counter()
recorded_inclusive = collections.Counter()
edge_cost = collections.Counter()
edge_calls = collections.Counter()
current = callee = None
pending = False
calls = 0
for line in lines:
    match = definition.fullmatch(line)
    if match:
        if line.startswith('fn='):
            current = int(match[1])
            pending = False
        else:
            callee = int(match[1])
        continue
    if line.startswith('calls='):
        calls = int(line[6:].split()[0])
        pending = True
        continue
    fields = line.split()
    if len(fields) != len(positions) + 1 or not fields[-1].isdigit():
        continue
    if not fields[0] or fields[0][0] not in '*+-0123456789':
        continue
    cost = int(fields[-1])
    assert current is not None
    if pending:
        assert callee is not None
        edge_cost[(current, callee)] += cost
        edge_calls[(current, callee)] += calls
        recorded_inclusive[current] += cost
        pending = False
    else:
        self_cost[current] += cost
        recorded_inclusive[current] += cost
assert sum(self_cost.values()) == result['guest_instructions']

def aggregate(costs):
    named = collections.Counter()
    for ident, cost in costs.items():
        named[definitions[ident]] += cost
    return named

own = aggregate(self_cost)
inclusive = aggregate(recorded_inclusive)
denominator = result['guest_instructions']
top = lambda costs: [{'name': name, 'instructions': cost, 'percent_of_total': 100 * cost / denominator}
                     for name, cost in costs.most_common(30)]
selected = ('__fn_util__NameIndex__chain', '__fn_ssalive__compute_prepared', '__fn_ssa__block_positions',
            '__fn_ssa__inst_defs', '__fn_semreach__walk', '__fn_ssadeps__analyze_checked',
            '__fn_semlower__substitution', '__fn_semsource__produce_module', '__fern_arr_box')
summary = {'guest_instructions': denominator, 'defined_functions': len(definitions), 'address_functions': len(addresses), 'unique_address_values': len(set(addresses.values())), 'address_context_labels': len(contexts),
           'resolved_address_functions': len(addresses) - len(unresolved), 'unresolved_addresses': unresolved,
           'self_cost_sum_matches_summary': True, 'self_top': top(own), 'recorded_inclusive_top': top(inclusive),
           'selected': {name: {'self_instructions': own[name], 'recorded_inclusive_instructions': inclusive[name],
                               'recorded_calls': sum(n for (caller, callee), n in edge_calls.items() if definitions[callee] == name)}
                        for name in selected},
           'limits': 'Recursion-context labels remain in the named copy and merge only in source-function cost aggregates. Single cold instruction diagnosis, not a timing or speedup comparison. Recorded inclusive costs overlap and can reflect recursion. Tail calls and inlining limit source attribution. Recorded calls omit inlined calls. None of these costs is established as avoidable.'}
(root / 'cold-analysis.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps({k: v for k, v in summary.items() if k not in ('recorded_inclusive_top', 'selected')}, indent=2))

(root / "all-function-costs.json").write_text(json.dumps({"self": dict(own), "recorded_inclusive": dict(inclusive)}, indent=2) + "\n")
