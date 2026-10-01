#!/usr/bin/env python3
"""Merge freshly generated E2E counters without changing baseline statements."""

import pathlib
import re
import sys


BLOCK = re.compile(r"^(\S+:\d+\.\d+,\d+\.\d+) (\d+) (\d+)$")


def read_profile(path):
    lines = pathlib.Path(path).read_text().splitlines()
    if not lines or lines[0] != "mode: atomic":
        raise ValueError(f"expected atomic coverage profile: {path}")
    blocks = {}
    for line in lines[1:]:
        match = BLOCK.fullmatch(line)
        if match is None:
            raise ValueError(f"invalid coverage block in {path}")
        location, statements, count = match.groups()
        statements, count = int(statements), int(count)
        if location in blocks:
            if blocks[location][0] != statements:
                raise ValueError(f"inconsistent statement count: {location}")
            count += blocks[location][1]
        blocks[location] = (statements, count)
    return blocks


def main():
    if len(sys.argv) < 4:
        raise ValueError("usage: merge-migration-coverage.py OUTPUT BASE EXTRA...")
    output, base, *extras = sys.argv[1:]
    blocks = read_profile(base)
    for extra in extras:
        for location, (statements, count) in read_profile(extra).items():
            if location not in blocks or blocks[location][0] != statements:
                raise ValueError(f"E2E profile differs from baseline source blocks: {location}")
            blocks[location] = (statements, blocks[location][1] + count)
    text = "mode: atomic\n" + "".join(
        f"{location} {statements} {count}\n"
        for location, (statements, count) in blocks.items()
    )
    pathlib.Path(output).write_text(text)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
