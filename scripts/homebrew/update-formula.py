#!/usr/bin/env python3
"""Generate dpg.rb from dpg.rb.tmpl by substituting version and sha256 values.

Usage:
  update-formula.py <version> <checksums.txt> <template> <output>

Arguments:
  version       Bare semver string, e.g. 0.5.2
  checksums.txt Path to the sha256sum output file from the release
  template      Path to dpg.rb.tmpl
  output        Path to write the filled formula, e.g. Formula/dpg.rb
"""
import sys
import pathlib
import string


def parse_checksums(text: str) -> dict[str, str]:
    shas: dict[str, str] = {}
    for line in text.strip().splitlines():
        line = line.strip()
        if not line:
            continue
        sha, name = line.split()
        shas[name] = sha
    return shas


def main() -> None:
    if len(sys.argv) != 5:
        sys.exit(__doc__)

    version, checksums_path, template_path, output_path = sys.argv[1:]

    shas = parse_checksums(pathlib.Path(checksums_path).read_text())

    required = {
        "dpg-darwin-amd64.tar.gz": "SHA_DARWIN_AMD64",
        "dpg-darwin-arm64.tar.gz": "SHA_DARWIN_ARM64",
        "dpg-linux-amd64.tar.gz":  "SHA_LINUX_AMD64",
        "dpg-linux-arm64.tar.gz":  "SHA_LINUX_ARM64",
    }
    missing = [k for k in required if k not in shas]
    if missing:
        sys.exit(f"error: missing checksums for: {', '.join(missing)}")

    template = string.Template(pathlib.Path(template_path).read_text())
    result = template.substitute(
        VERSION=version,
        SHA_DARWIN_AMD64=shas["dpg-darwin-amd64.tar.gz"],
        SHA_DARWIN_ARM64=shas["dpg-darwin-arm64.tar.gz"],
        SHA_LINUX_AMD64=shas["dpg-linux-amd64.tar.gz"],
        SHA_LINUX_ARM64=shas["dpg-linux-arm64.tar.gz"],
    )

    out = pathlib.Path(output_path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(result)
    print(f"wrote {out}")


if __name__ == "__main__":
    main()
