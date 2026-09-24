#!/usr/bin/env python3
"""Compare core configuration and old UI form with the reviewed contract.
No source edits or baseline updates are performed; --print emits a candidate
snapshot for explicit review, not evidence that an upgrade is compatible.
"""
import argparse
import difflib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]


def snapshot(root):
    text = (root / 'wing/dae-core/config/config.go').read_text()
    structs = {}
    for name in ('Global', 'Dns', 'Group', 'Routing'):
        match = re.search(r'^type ' + name + r' struct \{\n(.*?)^\}', text, re.M | re.S)
        if not match:
            raise ValueError(f'Cannot locate core {name}; review parser and upstream shape')
        fields = []
        for line in match[1].splitlines():
            if not line.strip() or line.lstrip().startswith('//'):
                continue
            field = re.match(r'\s*(\w+)\s+(.+?)\s+`([^`]+)`(?:\s*//.*)?$', line)
            if not field:
                raise ValueError(f'Unrecognized {name} field; explicit review required')
            fields.append({'name': field[1], 'type': field[2], 'tags': field[3]})
        structs[name] = fields
    ui = (root / 'apps/web/src/components/ConfigFormModal.tsx').read_text()
    form = re.search(r'const schema = z.object\(\{\n(.*?)\n\}\)', ui, re.S)
    if not form:
        raise ValueError('Cannot locate old UI form schema; explicit review required')
    return {'schema': 1, 'core_config': structs, 'old_ui_form': form[1].splitlines()}


def check(root):
    baseline = json.loads((root / 'compatibility/config-contract.json').read_text())
    current = snapshot(root)
    if current != baseline:
        before = json.dumps(baseline, indent=2, ensure_ascii=False).splitlines()
        after = json.dumps(current, indent=2, ensure_ascii=False).splitlines()
        diff = '\n'.join(difflib.unified_diff(before, after, fromfile='reviewed', tofile='candidate'))
        raise ValueError('Configuration contract changed; review migration/defaults/UI preservation before accepting:\n' + diff)
    return {'result': 'PASS', 'scope': 'field types, tags/defaults and old UI form; not runtime semantics'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--print', action='store_true', dest='print_snapshot')
    args = parser.parse_args()
    try:
        print(json.dumps(snapshot(ROOT) if args.print_snapshot else check(ROOT), indent=2))
    except (OSError, ValueError, KeyError) as exc:
        sys.exit(str(exc))
