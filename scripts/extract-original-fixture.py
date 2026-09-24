"""Extract exactly one ELF from the already checksum-verified official archive."""
from pathlib import Path
import os
import zipfile
assert os.environ.get("GITHUB_ACTIONS") == "true", "disposable CI only"
with zipfile.ZipFile(".artifacts/original-daed.zip") as archive:
    candidates = []
    for item in archive.infolist():
        if item.is_dir():
            continue
        with archive.open(item) as f:
            if f.read(4) == b"\x7fELF":
                candidates.append(item)
    assert len(candidates) == 1, "ambiguous executable archive"
    data = archive.read(candidates[0])
    target = Path(".artifacts/original-daed")
    target.write_bytes(data)
    target.chmod(0o700)
