"""Decide first immutable build or exact-source reuse. No push or tag mutation."""
import argparse
import json
import re
import subprocess
from pathlib import Path

TREE_LABEL = 'io.bixelventures.podconnect.source-tree'
ARCH_LABEL = 'io.bixelventures.podconnect.arch'
ARCHES = {'aarch64': 'arm64', 'amd64': 'amd64'}


def decision(returncode, output, error, *, reference, tree, arch):
    if arch not in ARCHES or re.fullmatch(r'[a-f0-9]{40}', tree) is None:
        raise ValueError('invalid expected source identity')
    if returncode:
        # Only a conclusive registry missing-manifest response admits a first build.
        # Auth/network/rate-limit/server errors, including mixed error text, reject.
        text = error.strip()
        missing = re.fullmatch(r'ERROR: ' + re.escape(reference) +
            r': (?:not found|manifest unknown)', text)
        if missing:
            return 'build'
        raise ValueError('image inspection failed; absence is not established')
    image = json.loads(output)
    if not isinstance(image, dict):
        raise ValueError('missing single-platform image metadata')
    labels = image.get('config', {}).get('Labels', {})
    if (image.get('os') != 'linux' or image.get('architecture') != ARCHES[arch]
            or labels.get(TREE_LABEL) != tree or labels.get(ARCH_LABEL) != arch):
        raise ValueError('existing immutable image does not match exact source and architecture')
    return 'reuse'


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--arch', choices=ARCHES, required=True)
    parser.add_argument('--version', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    if re.fullmatch(r'\d+\.\d+\.\d+', args.version) is None:
        raise ValueError('invalid release version')
    # Authored subtree identity survives a squash/merge commit and excludes unrelated files.
    tree = subprocess.check_output(['git', 'rev-parse', 'HEAD:podconnect'], text=True).strip()
    reference = f'ghcr.io/bixelventures/{args.arch}-podconnect:{args.version}'
    result = subprocess.run(['docker', 'buildx', 'imagetools', 'inspect', '--format',
                             '{{json .Image}}', reference], capture_output=True, text=True, timeout=60)
    action = decision(result.returncode, result.stdout, result.stderr,
                      reference=reference, tree=tree, arch=args.arch)
    with Path(args.output).open('a') as output:
        output.write(f'action={action}\nsource_tree={tree}\n')
    print(f'{action}: {reference} source_tree={tree}')


if __name__ == '__main__':
    main()
