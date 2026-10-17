import os
import shutil
from pathlib import Path
import subprocess

import pytest

from .harness import Product


@pytest.fixture(autouse=True)
def cleanup_private_case(tmp_path):
    yield
    # Autouse setup precedes product fixtures, so their processes close first.
    shutil.rmtree(tmp_path)


@pytest.fixture(scope="session")
def binaries(tmp_path_factory):
    root = Path(__file__).resolve().parents[2]
    output = tmp_path_factory.mktemp("binaries")
    env = os.environ | {"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}
    for name, package in [("haeryong", "./cmd/haeryong"), ("f12", "./tests/fixtures/f12")]:
        subprocess.run(["go", "build", "-race", "-o", str(output / name), package],
                       cwd=root, env=env, check=True, timeout=120, capture_output=True)
    return output


@pytest.fixture
def product(binaries, tmp_path):
    value = Product(binaries / "haeryong", tmp_path)
    try:
        value.initialize()
        value.start()
        yield value
    finally:
        value.close()


@pytest.fixture
def owner(product):
    return product.owner()


@pytest.fixture(scope="session")
def canonical_f12(binaries, tmp_path_factory):
    import json

    root = tmp_path_factory.mktemp("canonical-input") / "f12"
    subprocess.run([str(binaries / "f12"), str(root)], check=True, timeout=30, capture_output=True)
    value = json.loads((root / "manifest.json").read_text())
    assert value["archive_bytes"] == 12_768_177
    assert value["expanded_bytes"] == 55_050_821
    assert len(value["files"]) == 1027
    return value
