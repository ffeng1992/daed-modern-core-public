#!/usr/bin/env python3
"""Fail closed on target-kernel logs and future full-Debian evidence reports."""
import argparse
import json
import pathlib
import re
import sys

EXPECTED_KERNEL = (
    'TestIsolatedRuntimeCoordinator',
    'TestIsolatedLANForwarding',
    'TestIsolatedGraphQLRuntime',
)
REQUIRED_STAGES = {
    'A': {'clean_source', 'formal_image', 'compose_start', 'web_health'},
    'B': {'account_init', 'management_write', 'config_persistence'},
    'C': {'dns_udp', 'dns_tcp', 'proxy_tcp', 'proxy_udp', 'direct', 'bypass_negative_control'},
    'D': {'stop', 'restart', 'failure_recovery', 'config_retained', 'network_state'},
}


def check_kernel(log, artifact):
    if not artifact.is_file() or artifact.stat().st_size == 0:
        raise ValueError(f'required artifact missing or empty: {artifact}')
    text = log.read_text()
    found = {}
    for line in text.splitlines():
        match = re.fullmatch(r'--- (PASS|FAIL|SKIP): (Test\w+) \([^)]*\)', line.strip())
        if match and match.group(2) in EXPECTED_KERNEL:
            found.setdefault(match.group(2), []).append(match.group(1))
    for name in EXPECTED_KERNEL:
        if found.get(name) != ['PASS']:
            raise ValueError(f'{name}: expected one PASS, found {found.get(name, [])}')
    if re.search(r'(?m)^FAIL(?:\s|$)', text) or not re.search(r'(?m)^PASS$', text):
        raise ValueError('Go test suite did not finish cleanly')
    return {'status': 'PASS', 'tests': list(EXPECTED_KERNEL)}


def check_full(path):
    report = json.loads(path.read_text())
    if report.get('product_source_sha') != 'e7347b2becc4b4a82adda0b23ce3c9d0621cd5ea':
        raise ValueError('wrong product source SHA')
    if report.get('runtime_image_origin') != 'Dockerfile+docker-compose.yml':
        raise ValueError('formal Dockerfile/Compose image origin missing')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', report.get('runtime_image_id', '')):
        raise ValueError('real runtime image ID missing')
    if not report.get('guest_kernel') or not report.get('docker_version') or not report.get('compose_version'):
        raise ValueError('guest runtime versions missing')
    stages = report.get('stages', {})
    if set(stages) != set(REQUIRED_STAGES):
        raise ValueError('A/B/C/D stages incomplete')
    for stage, checks in REQUIRED_STAGES.items():
        row = stages[stage]
        if row.get('status') != 'PASS':
            raise ValueError(f'stage {stage} not PASS: {row.get("status")}')
        got = row.get('checks', {})
        if set(got) != checks:
            raise ValueError(f'stage {stage} checks missing or unexpected: {sorted(checks - set(got))}')
        for name, evidence in got.items():
            if evidence.get('status') != 'PASS' or not evidence.get('evidence'):
                raise ValueError(f'stage {stage}/{name} lacks PASS evidence')
            evidence_path = (path.parent / evidence['evidence']).resolve()
            if not evidence_path.is_relative_to(path.parent.resolve()) or not evidence_path.is_file() or evidence_path.stat().st_size == 0:
                raise ValueError(f'stage {stage}/{name} evidence file absent, empty or outside report directory')
    comparison = report.get('official_daed_comparison', {})
    if comparison.get('status') not in ('PASS', 'FAIL', 'NOT_RUN'):
        raise ValueError('official comparison status must be explicit')
    if comparison['status'] == 'NOT_RUN' and not comparison.get('reason'):
        raise ValueError('official comparison NOT_RUN requires reason')
    return {'candidate': 'PASS', 'official_daed_comparison': comparison['status']}


def main():
    parser = argparse.ArgumentParser()
    subs = parser.add_subparsers(dest='mode', required=True)
    p = subs.add_parser('kernel-log')
    p.add_argument('log', type=pathlib.Path)
    p.add_argument('artifact', type=pathlib.Path)
    p = subs.add_parser('full-report')
    p.add_argument('report', type=pathlib.Path)
    args = parser.parse_args()
    try:
        result = check_kernel(args.log, args.artifact) if args.mode == 'kernel-log' else check_full(args.report)
    except (ValueError, OSError, json.JSONDecodeError) as exc:
        print(f'FAIL: {exc}', file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == '__main__':
    sys.exit(main())
