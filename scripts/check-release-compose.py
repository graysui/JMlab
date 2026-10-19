"""Validate the minimal release Compose without reading deployment data or starting Docker."""

from pathlib import Path
import json
import os
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
COMPOSE = ROOT / "deploy" / "docker-compose.release.yml"


class ReleaseComposeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        docker = shutil.which("docker")
        if not docker:
            raise RuntimeError("Docker Compose CLI is required; no Docker daemon or container is used.")
        environment = {
            name: value for name, value in os.environ.items()
            if not name.upper().startswith(("JMLAB_", "COMPOSE_"))
        }
        with tempfile.TemporaryDirectory(prefix="jmlab-compose-check-") as directory:
            empty_env = Path(directory) / "empty.env"
            empty_env.write_text("", encoding="utf-8")
            result = subprocess.run(
                [docker, "compose", "--env-file", str(empty_env),
                 "--project-directory", directory, "-f", str(COMPOSE),
                 "config", "--format", "json"],
                cwd=directory, env=environment, capture_output=True,
                check=False, timeout=30,
            )
        if result.returncode:
            raise RuntimeError("Release Compose could not render with an empty environment file.")
        cls.configuration = json.loads(result.stdout)
        cls.service = cls.configuration["services"]["jmlab"]

    def test_only_required_user_settings(self):
        self.assertEqual(set(self.configuration["services"]), {"jmlab"})
        environment = self.service["environment"]
        self.assertEqual(set(environment), {"JMLAB_PUBLIC_URL", "JMLAB_BOOTSTRAP_ADMIN_PASSWORD"})
        self.assertEqual(environment["JMLAB_PUBLIC_URL"], "http://localhost:8097")
        self.assertEqual(environment["JMLAB_BOOTSTRAP_ADMIN_PASSWORD"], "")

    def test_uses_image_defaults_without_shell_or_extra_files(self):
        for key in ("entrypoint", "command", "env_file", "build"):
            with self.subTest(setting=key):
                self.assertFalse(self.service.get(key), f"Release Compose must not set {key}.")
        self.assertRegex(self.service["image"], r"^graysui/jmlab:v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$")
        self.assertEqual(self.service["restart"], "unless-stopped")

    def test_keeps_data_and_backup_in_named_volumes(self):
        self.assertEqual(set(self.configuration["volumes"]), {"jmlab-data", "jmlab-backup"})
        volumes = self.service["volumes"]
        self.assertEqual(len(volumes), 2)
        self.assertTrue(all(volume["type"] == "volume" for volume in volumes))
        self.assertEqual({(volume["source"], volume["target"]) for volume in volumes},
                         {("jmlab-data", "/data"), ("jmlab-backup", "/backup")})

    def test_publishes_the_default_http_port(self):
        ports = self.service["ports"]
        self.assertEqual(len(ports), 1)
        self.assertEqual(ports[0]["target"], 8097)
        self.assertEqual(str(ports[0]["published"]), "8097")
        self.assertEqual(ports[0]["protocol"], "tcp")


if __name__ == "__main__":
    unittest.main(verbosity=2)
