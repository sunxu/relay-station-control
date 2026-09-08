"""Contract tests for the permanent protected Directory acceptance config.

The harness entrypoint will expose ``validate_persistent_config(path, cfg)``.
These tests intentionally keep the ordinary ``_cfg``/HTTP tests free to use
temporary fixtures; only the CLI's permanent-file admission is covered here.
"""
import os
from pathlib import Path
import tempfile
import unittest
import sys

sys.path.insert(0, str(Path(__file__).parent))
import directory_http


class DirectoryConfigPathTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix=".relay-config-test-", dir=Path.home())
        self.root = Path(self.tmp.name) / "private"
        self.root.mkdir(mode=0o700)
        self.config = self.root / "directory.json"
        self.config.write_text("{}\n")
        self.config.chmod(0o600)
        self.cfg = {
            "control_url": "https://control.example.invalid",
            "gateway_instance_id": "00000000-0000-0000-0000-000000000001",
            "node_instance_id": "00000000-0000-0000-0000-000000000002",
            "gateway_account_id": "9007199254740993",
        }

    def tearDown(self):
        self.tmp.cleanup()

    def validate(self, path=None, cfg=None):
        validator = getattr(directory_http, "validate_persistent_config", None)
        if validator is None:
            self.fail("directory_http.validate_persistent_config is not wired")
        return validator(path or self.config, cfg or self.cfg)

    def test_valid_protected_inputs_are_admitted(self):
        for key in ("session_cookie_file", "password_file", "mfa_code_file",
                    "baseline_file", "directory_token_file", "data_plane_request_file",
                    "data_plane_token_file"):
            path = self.root / key
            path.write_text("protected\n")
            path.chmod(0o600)
            self.cfg[key] = str(path)
        self.validate()

    def test_repository_and_known_temporary_paths_are_rejected(self):
        candidates = [
            Path(__file__).resolve(),
            Path(__file__).resolve().parents[3] / "control" / "config.yaml",
            Path(__file__).resolve().parents[3] / "ops" / "dev" / "compose.yaml",
            Path("/tmp/directory.json"),
            Path("/private/tmp/directory.json"),
            Path("/Volumes/DevRAM/tmp/directory.json"),
            Path("/var/folders/test/directory.json"),
        ]
        for candidate in candidates:
            with self.subTest(candidate=str(candidate)):
                with self.assertRaises(directory_http.HarnessError):
                    self.validate(candidate)

    def test_every_file_reference_is_checked_not_only_main_config(self):
        references = ("ca_file", "directory_ca_file", "data_plane_ca_file",
                      "session_cookie_file", "password_file", "mfa_code_file",
                      "baseline_file", "directory_token_file",
                      "data_plane_request_file", "data_plane_token_file")
        for key in references:
            with self.subTest(key=key):
                self.cfg[key] = str(Path("/tmp") / (key + ".secret"))
                with self.assertRaises(directory_http.HarnessError):
                    self.validate()
                self.cfg.pop(key)

    def test_cross_parent_symlink_is_rejected(self):
        outside = Path(self.tmp.name) / "outside"
        outside.mkdir(mode=0o700)
        link = self.root / "linked-parent"
        link.symlink_to(outside, target_is_directory=True)
        candidate = link / "config.json"
        with self.assertRaises(directory_http.HarnessError):
            self.validate(candidate)

    def test_private_file_and_directory_permissions_are_enforced(self):
        bad_dir = Path(self.tmp.name) / "public"
        bad_dir.mkdir(mode=0o755)
        bad_file = bad_dir / "secret"
        bad_file.write_text("secret\n")
        bad_file.chmod(0o600)
        self.cfg["password_file"] = str(bad_file)
        with self.assertRaises(directory_http.HarnessError):
            self.validate()

        bad_file.chmod(0o604)
        bad_dir.chmod(0o700)
        with self.assertRaises(directory_http.HarnessError):
            self.validate()

    def test_ca_may_be_public_readable_but_not_symlink_or_other_writable(self):
        ca = self.root / "ca.pem"
        ca.write_text("certificate\n")
        ca.chmod(0o644)
        self.cfg["ca_file"] = str(ca)
        self.validate()

        ca.chmod(0o646)
        with self.assertRaises(directory_http.HarnessError):
            self.validate()
        ca.unlink()
        ca.symlink_to(self.config)
        with self.assertRaises(directory_http.HarnessError):
            self.validate()


if __name__ == "__main__":
    unittest.main()
