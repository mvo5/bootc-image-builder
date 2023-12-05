import json
import os
import pathlib
import subprocess

import pytest

# local test utils
import testutil


@pytest.fixture(name="output_path")
def output_path_fixture(tmp_path):
    output_path = tmp_path / "output"
    output_path.mkdir(exist_ok=True)
    return output_path


@pytest.fixture(name="config_json")
def config_json_fixture(output_path):
    CFG = {
        "blueprint": {
            "customizations": {
                "user": [
                    {
                        "name": "test",
                        "password": "password",
                        "groups": ["wheel"],
                    },
                ],
            },
        },
    }
    config_json_path = output_path / "config.json"
    config_json_path.write_text(json.dumps(CFG), encoding="utf-8")
    return config_json_path


@pytest.mark.skipif(os.getuid() != 0, reason="needs root")
@pytest.mark.skipif(not testutil.has_executable("podman"), reason="need podman")
@pytest.mark.parametrize("arch", ["arm64", "amd64"])
def test_smoke(output_path, config_json, arch):
    # build local container
    subprocess.check_call([
        "podman", "build",
        "--arch", arch,
        "-f", "Containerfile",
        "-t", "osbuild-deploy-container-test",
    ])
    cursor = testutil.journal_cursor()
    # and run container to deploy an image into output/disk.qcow2
    subprocess.check_call([
        "podman", "run", "--rm",
        "--arch", arch,
        "--privileged",
        "--security-opt", "label=type:unconfined_t",
        "-v", f"{output_path}:/output",
        "osbuild-deploy-container-test",
        "quay.io/centos-bootc/centos-bootc:stream9",
        "--config", "/output/config.json",
    ])
    # check that there are no denials
    journal_output = testutil.journal_after_cursor(cursor)
    assert journal_output != ""
    # TODO: actually check this once https://github.com/osbuild/images/pull/287
    #       is merged
    generated_img = pathlib.Path(output_path) / "qcow2/disk.qcow2"
    assert generated_img.exists(), f"output file missing, dir content: {os.listdir(os.fspath(output_path))}"
    # TODO: boot and do basic checks, see
    # https://github.com/osbuild/osbuild-deploy-container/compare/main...mvo5:integration-test?expand=1
