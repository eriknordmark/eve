#!/usr/bin/env python3
"""Filter a Go coverage text file to remove entries with conflicting NumStmt.

When coverage data from different binary builds is merged, the same source
block may appear with different NumStmt values (e.g. after a code change).
'go tool cover -func' rejects such files with "inconsistent NumStmt".

This script keeps the first occurrence of each (file, range) key and drops
subsequent lines where NumStmt differs, printing a warning to stderr.

Usage:
    python3 filter-coverage-conflicts.py <input> [output]
    python3 filter-coverage-conflicts.py < input.txt > output.txt
"""

import sys


def filter_conflicts(infile, outfile):
    seen = {}       # key -> numstmt of first occurrence
    dropped = 0

    for line in infile:
        stripped = line.rstrip('\n')
        if not stripped or stripped.startswith('mode:'):
            outfile.write(line)
            continue

        parts = stripped.split()
        if len(parts) != 3:
            outfile.write(line)
            continue

        key, numstmt, _ = parts
        prev = seen.get(key)
        if prev is None:
            seen[key] = numstmt
            outfile.write(line)
        elif prev != numstmt:
            dropped += 1
            if dropped <= 10:
                print(
                    f"warning: dropping {key} numstmt={numstmt} "
                    f"(conflicts with first-seen numstmt={prev})",
                    file=sys.stderr,
                )
        else:
            outfile.write(line)

    if dropped > 10:
        print(
            f"warning: {dropped} conflicting entries dropped in total",
            file=sys.stderr,
        )
    elif dropped:
        print(f"warning: {dropped} conflicting entries dropped", file=sys.stderr)


def main():
    args = sys.argv[1:]
    if args and args[0] in ('-h', '--help'):
        print(__doc__)
        sys.exit(0)

    if not args:
        filter_conflicts(sys.stdin, sys.stdout)
    elif len(args) == 1:
        with open(args[0]) as inf:
            filter_conflicts(inf, sys.stdout)
    elif len(args) == 2:
        with open(args[0]) as inf, open(args[1], 'w') as outf:
            filter_conflicts(inf, outf)
    else:
        print(f"usage: {sys.argv[0]} [input [output]]", file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()
