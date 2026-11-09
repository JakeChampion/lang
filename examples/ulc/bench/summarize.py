#!/usr/bin/env python3
"""Combine ULC reports and estimate uncertainty for a fixed benchmark suite."""
import argparse
from datetime import datetime, timezone
import json
import math
from pathlib import Path
import random
import statistics


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('reports', type=Path, nargs='+')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--bootstrap', type=int, default=2000)
    parser.add_argument('--seed', type=int, default=417)
    parser.add_argument('--comparison', choices=('C', 'Baseline'), default='C')
    args = parser.parse_args()
    if args.bootstrap < 100:
        parser.error('use at least 100 bootstrap repetitions')
    reports = [json.loads(path.read_text()) for path in args.reports]
    reports.sort(key=lambda r: (r['seconds'], r['threads'], r['block_size']))
    if len({r['source_commit'] for r in reports}) != 1:
        parser.error('use reports from one source commit')
    rows = [row for report in reports for row in report['results']]
    if any(args.comparison not in row['seconds'] for row in rows):
        parser.error('the selected comparison is missing from a report')
    observations = [(row['seconds'][args.comparison], row['seconds']['Fern']) for row in rows]
    for c, fern in observations:
        if len(c) != len(fern) or len(c) < 3 or any(not math.isfinite(x) or x <= 0 for x in c + fern):
            parser.error('each case needs matching positive finite timing samples')
    ratios = [statistics.median(c) / statistics.median(fern) for c, fern in observations]
    rng = random.Random(args.seed)
    estimates = []
    non_silent_estimates = []
    for _ in range(args.bootstrap):
        sampled = []
        for c, fern in observations:
            indices = rng.choices(range(len(c)), k=len(c))
            sampled.append(statistics.median(c[i] for i in indices) / statistics.median(fern[i] for i in indices))
        estimates.append(statistics.geometric_mean(sampled))
        non_silent_estimates.append(statistics.geometric_mean(ratio for row, ratio in zip(rows, sampled) if row['fixture'] != 'silence'))
    estimates.sort()
    non_silent_estimates.sort()
    summary = {
        'comparison': args.comparison,
        'cases': len(rows),
        'fern_lower_medians': sum(ratio > 1 for ratio in ratios),
        'geometric_mean_ratio': statistics.geometric_mean(ratios),
        'geometric_mean_without_silence': statistics.geometric_mean(ratio for row, ratio in zip(rows, ratios) if row['fixture'] != 'silence'),
        'bootstrap_repetitions': args.bootstrap,
        'bootstrap_seed': args.seed,
        'interval_scope': 'paired repetitions within each fixed case',
        'geometric_mean_without_silence_95_percent_interval': [non_silent_estimates[int(p * (len(non_silent_estimates) - 1))] for p in (0.025, 0.975)],
        'geometric_mean_95_percent_interval': [estimates[int(p * (len(estimates) - 1))] for p in (0.025, 0.975)],
    }
    report = {'summarized_date_utc': datetime.now(timezone.utc).date().isoformat(), 'summary': summary, 'reports': reports}
    args.output.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    main()
