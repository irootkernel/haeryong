"""Run the selected contract matrix through its canonical pytest handler."""
import argparse
from pathlib import Path
import sys

import pytest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()
    if not args.report.is_absolute() or args.report.exists():
        parser.error("report must be a new explicit absolute private file")
    return pytest.main([str(Path(__file__).parent),
                        "--contract-report", str(args.report)])


if __name__ == "__main__":
    sys.exit(main())
