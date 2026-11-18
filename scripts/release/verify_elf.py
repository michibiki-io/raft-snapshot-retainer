"""Check the ELF64 machine field for a supported Linux release architecture."""

from pathlib import Path
import sys


def main():
    arch, binary = sys.argv[1:]
    expected = {"amd64": 62, "arm64": 183}[arch]
    with Path(binary).open("rb") as executable:
        header = executable.read(64)
    if len(header) != 64 or header[:6] != b"\x7fELF\x02\x01":
        raise SystemExit(f"{binary}: expected a little-endian ELF64 executable")
    machine = int.from_bytes(header[18:20], "little")
    if machine != expected:
        raise SystemExit(f"{binary}: ELF machine {machine}, expected {expected} ({arch})")
    print(f"{binary}: ELF machine {machine} ({arch})")


if __name__ == "__main__":
    main()
