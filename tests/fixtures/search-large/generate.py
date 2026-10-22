"""Independent SC16 static-body completeness oracle; never modifies canonical F12."""
import hashlib
import json
from pathlib import Path

CHUNK = 32768
STRIDE = 28673


def generate(destination):
    destination = Path(destination)
    destination.mkdir()
    size = 42 * 1024 * 1024
    body = bytearray((b"p" * 1023 + b" ") * (size // 1024))
    markers = []
    for number, window in enumerate((0, 600, 1200), 1):
        offset = window * STRIDE + CHUNK - 1
        marker = f"searchLargeBoundary{number:04}End"
        body[offset:offset + len(marker)] = marker.encode()
        markers.append({"literal": marker, "normalized_offset": offset})
    tail = "searchLargeFinalTailOnly"
    # Preserve collapsed scalar offsets: replace the final padding space.
    body[-1:] = tail.encode()
    markers.append({"literal": tail, "normalized_offset": size - 1})
    payload = b"<html><body><p>" + body + b"</p></body></html>"
    for row in markers:
        assert payload.count(row["literal"].encode()) == 1
    (destination / "large.html").write_bytes(payload)
    return {"bytes": len(payload), "sha256": hashlib.sha256(payload).hexdigest(),
            "markers": markers, "chunk": CHUNK, "overlap": CHUNK - STRIDE,
            "scope": "separate SC16 body fixture, not canonical F12"}


if __name__ == "__main__":
    import sys
    print(json.dumps(generate(sys.argv[1]), sort_keys=True))
