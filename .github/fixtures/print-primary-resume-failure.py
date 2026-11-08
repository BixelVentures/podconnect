from pathlib import Path
import re

prefix = Path("/tmp/owntone/pc52-selected-resume-actual-c-primary-same-target")
markers = re.compile(
    r"FAIL|Resume|radio|preflight|handoff|park|barrier|RTP|teardown|unknown|quarantine|error",
    re.IGNORECASE,
)
for kind in ("native", "c"):
    log = Path(f"{prefix}-{kind}.log")
    if not log.exists():
        continue
    lines = [line for line in log.read_text(errors="replace").splitlines() if markers.search(line)]
    print(f"=== {kind} retained Resume ===")
    for line in lines[-80:]:
        print(re.sub(r"[0-9a-fA-F]{32,}", "[redacted]", line))
