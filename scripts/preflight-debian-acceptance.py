#!/usr/bin/env python3
"""Read-only gate for a future full Debian VM run; does not create or boot a VM."""
import argparse
import hashlib
import json
import pathlib
import sys


def sha256(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(block)
    return digest.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--manifest', type=pathlib.Path, default=pathlib.Path('acceptance/debian-13.7-amd64.json'))
    parser.add_argument('--iso', required=True, type=pathlib.Path)
    parser.add_argument('--provenance', required=True, type=pathlib.Path)
    args = parser.parse_args()
    try:
        manifest = json.loads(args.manifest.read_text())
        provenance = json.loads(args.provenance.read_text())
        if args.iso.name != manifest['installer']['name'] or sha256(args.iso) != manifest['installer']['sha256']:
            raise ValueError('Debian ISO name or SHA-256 mismatch')
        if provenance['product_source_sha'] != manifest['product_source_sha']:
            raise ValueError('product source SHA mismatch')
        image = provenance['image_ids']['runtime']
        if not image.startswith('sha256:') or len(image) != 71:
            raise ValueError('formal runtime image ID missing')
        if manifest['runtime_image_origin'] != 'Dockerfile+docker-compose.yml':
            raise ValueError('formal image origin mismatch')
    except (OSError, KeyError, ValueError, json.JSONDecodeError) as exc:
        print(f'NOT_READY: {exc}', file=sys.stderr)
        return 1
    print('READY_FOR_SEPARATELY_AUTHORIZED_VM_RUN: ISO, product source and formal image provenance checked')
    return 0


if __name__ == '__main__':
    sys.exit(main())
