"""Freeze exact candidate; apply test-only HA-worker composition overlay.
No production source is edited. Build with the candidate Go version.
"""

import argparse
import hashlib
import json
import pathlib
import shutil
import subprocess

p = argparse.ArgumentParser()
p.add_argument("--source-manager", type=pathlib.Path, required=True)
p.add_argument("--runtime-root", type=pathlib.Path, required=True)
a = p.parse_args()
source = a.source_manager.resolve(strict=True)
root = a.runtime_root.resolve()
root.mkdir(parents=True, exist_ok=True)
dest = root / "manager"
if dest.exists():
    raise SystemExit("refusing to replace an existing frozen manager snapshot")
shutil.copytree(source, dest, ignore=shutil.ignore_patterns(".git"))
pins = {
    f.name: hashlib.sha256(f.read_bytes()).hexdigest()
    for f in source.iterdir()
    if f.is_file()
}
(root / "manager-source-pins.json").write_text(json.dumps(pins, indent=2) + "\n")
overlay = pathlib.Path(__file__).resolve().parent / "radio-worker-test-overlay.patch"
subprocess.run(["git", "apply", "--check", str(overlay)], cwd=dest, check=True)
subprocess.run(["git", "apply", str(overlay)], cwd=dest, check=True)
for name, sha in pins.items():
    if (
        not name.endswith("_test.go")
        and hashlib.sha256((dest / name).read_bytes()).hexdigest() != sha
    ):
        raise SystemExit("production source changed: " + name)
(root / "manager-harness-delta.json").write_text(
    json.dumps(
        {
            "overlay_sha256": hashlib.sha256(overlay.read_bytes()).hexdigest(),
            "production_byte_identical": True,
            "modified_tests": [
                "radio_producer_chain_external_test.go",
                "radio_worker_runtime_external_test.go",
                "radio_producer_discovery_external_test.go",
            ],
        },
        indent=2,
    )
    + "\n"
)
