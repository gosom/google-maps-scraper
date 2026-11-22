#!/usr/bin/env python3
"""Run alternating fast/detailed CLI benchmarks; keep CSVs and logs locally."""
import argparse
import csv
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import statistics
import subprocess
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', required=True)
parser.add_argument('--output', required=True)
parser.add_argument('--runs', type=int, default=3)
args = parser.parse_args()
if args.runs < 1:
    parser.error('--runs must be positive')
binary = Path(args.binary).resolve()
output = Path(args.output).resolve()
output.mkdir(parents=True, exist_ok=False)
query = output / 'queries.txt'
query.write_text('cafes in Rhodes\n')
repo = Path(__file__).resolve().parents[2]
source_diff = subprocess.check_output(['git', 'diff', 'HEAD', '--', '*.go', 'go.mod', 'go.sum'], cwd=repo)
source_files = subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '--', '*.go', 'go.mod', 'go.sum'], cwd=repo, text=True).splitlines()
source_manifest = {name: hashlib.sha256((repo / name).read_bytes()).hexdigest() for name in sorted(set(source_files))}
cpu = next((line.split(':', 1)[1].strip() for line in Path('/proc/cpuinfo').read_text().splitlines() if line.startswith('model name')), 'unknown') if Path('/proc/cpuinfo').exists() else platform.processor()
report = {
    'date_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'platform': platform.platform(), 'cpu': cpu, 'logical_cpus': os.cpu_count(),
    'os_release': platform.freedesktop_os_release() if platform.system() == 'Linux' else {},
    'memory_kib': int(Path('/proc/meminfo').read_text().splitlines()[0].split()[1]) if Path('/proc/meminfo').exists() else None,
    'base_commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip(),
    'go_diff_sha256': hashlib.sha256(source_diff).hexdigest(),
    'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
    'source_tree_sha256': hashlib.sha256(json.dumps(source_manifest, sort_keys=True).encode()).hexdigest(),
    'query': 'cafes in Rhodes', 'runs': [],
}
(output / 'source.patch').write_bytes(source_diff)
env = dict(os.environ, DISABLE_TELEMETRY='1')
# No inherited shell proxy configuration for these direct-connection runs.
for key in ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'http_proxy', 'https_proxy', 'all_proxy'):
    env.pop(key, None)
for trial in range(1, args.runs + 1):
    modes = ('fast', 'detailed') if trial % 2 else ('detailed', 'fast')
    for mode in modes:
        stem = f'{mode}-{trial}'
        csv_path = output / f'{stem}.csv'
        cmd = [str(binary), '-input', str(query), '-results', str(csv_path),
               '-depth', '3', '-c', '4', '-geo', '36.4341,28.2176',
               '-zoom', '15', '-radius', '10000', '-lang', 'en',
               '-exit-on-inactivity', '60s']
        if mode == 'fast':
            cmd.append('-fast-mode')
        print(f'Starting {stem}', flush=True)
        start = time.monotonic()
        timed_out = False
        with (output / f'{stem}.log').open('w') as log:
            process = subprocess.Popen(cmd, stdout=log, stderr=subprocess.STDOUT, env=env, start_new_session=True)
            try:
                code = process.wait(timeout=600)
            except subprocess.TimeoutExpired:
                timed_out = True
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    code = process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    code = process.wait()
        elapsed = time.monotonic() - start
        rows = list(csv.DictReader(csv_path.open())) if csv_path.exists() else []
        errors = []
        for line in (output / f'{stem}.log').read_text().splitlines():
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue
            if event.get('level') in ('error', 'fatal', 'panic'):
                errors.append(event)
        record = {'mode': mode, 'trial': trial, 'seconds': round(elapsed, 3),
                  'rows': len(rows), 'unique_place_ids': len({r['place_id'] for r in rows if r['place_id']}),
                  'exit_code': code, 'timed_out': timed_out, 'errors': errors, 'command': cmd}
        report['runs'].append(record)
        (output / 'results.json').write_text(json.dumps(report, indent=2))
        print(json.dumps(record), flush=True)
        if timed_out or code != 0 or not rows or errors:
            raise SystemExit('Run needs investigation; logs and partial results retained.')
report['summary'] = {}
for mode in ('fast', 'detailed'):
    runs = [r for r in report['runs'] if r['mode'] == mode]
    report['summary'][mode] = {
        'median_seconds': statistics.median(r['seconds'] for r in runs),
        'min_seconds': min(r['seconds'] for r in runs),
        'max_seconds': max(r['seconds'] for r in runs),
        'row_counts': [r['rows'] for r in runs],
    }
(output / 'results.json').write_text(json.dumps(report, indent=2))
print(json.dumps(report['summary'], indent=2), flush=True)
