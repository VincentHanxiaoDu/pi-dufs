import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("dufs", Path(__file__).parents[1] / "skills/dufs/scripts/dufs.py")
dufs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dufs)


class ClientTests(unittest.TestCase):
    def test_paths(self):
        for path in ("../secret", "/root", "a//b", "a/./b", "a\\b", ""):
            with self.assertRaises(ValueError):
                dufs.remote_path(path)
        self.assertEqual(dufs.remote_path("demo/a #?.html"), "demo/a%20%23%3F.html")

    def test_configuration(self):
        with tempfile.TemporaryDirectory() as temp:
            secret = Path(temp) / "secret"
            secret.write_text("test-password\n")
            env = {"DUFS_CONFIG": temp + "/missing", "DUFS_URL": "https://example.test:8443", "DUFS_PASSWORD_FILE": str(secret)}
            with patch.dict(os.environ, env, clear=True):
                url, auth = dufs.settings()
                self.assertEqual(url, env["DUFS_URL"])
                self.assertEqual(dufs.base64.b64decode(auth), b"ai:test-password")
                for bad in ("http://example.test", "https://user:pass@example.test", "https://example.test?x=1", "https://example.test#x"):
                    with patch.dict(os.environ, {"DUFS_URL": bad}):
                        with self.assertRaises(ValueError):
                            dufs.settings()

    def test_redirect_blocked(self):
        with self.assertRaises(ValueError):
            dufs.NoRedirect().redirect_request(None, None, 302, "", {}, "https://other.test")

    def test_publish(self):
        client = dufs.Client.__new__(dufs.Client)
        client.url = "https://example.test"
        calls = []
        def request(method, path, data=None, size=None):
            calls.append((method, path, data.read() if data else None, size))
        client.request = request
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "index.html").write_text("<h1>Hello</h1>")
            (root / "app.js").write_text("console.log(1)")
            first = client.publish(root)
            second = client.publish(root)
            self.assertNotEqual(first["url"], second["url"])
            self.assertEqual(first["files"], 2)
            self.assertEqual([c[0] for c in calls], ["PUT", "PUT", "HEAD"] * 2)
            (root / ".env").write_text("secret")
            with self.assertRaises(ValueError):
                client.publish(root)
            (root / ".env").unlink()
            (root / "linked").symlink_to(root / "index.html")
            with self.assertRaises(ValueError):
                client.publish(root)
            self.assertEqual(len(calls), 6)


if __name__ == "__main__":
    unittest.main()
