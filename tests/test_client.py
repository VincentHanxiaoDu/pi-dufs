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
        for path in ("../secret", "/root", "a//b", "a/./b", "a\\b", "", "__dufs/login", "__dufs_v0.47.0__/index.js"):
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

    def client(self, stored=None):
        stored = {} if stored is None else stored
        client = dufs.Client.__new__(dufs.Client)
        client.url = "https://example.test"
        calls = []
        def request(method, path, data=None, size=None, create_only=False):
            calls.append((method, path, create_only))
            if method == "HEAD" and path not in stored:
                raise dufs.DufsError(404)
            if method == "PUT":
                self.assertTrue(create_only)
                if path in stored:
                    raise dufs.DufsError(412)
                stored[path] = data.read()
                self.assertEqual(len(stored[path]), size)
        client.request = request
        return client, calls, stored

    def test_exact_file_destination(self):
        client, calls, stored = self.client()
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / "local-name.md"
            source.write_text("# Hello")
            result = client.publish(source, "projects/xnoq/design/spec.md")
            self.assertEqual(list(stored), ["projects/xnoq/design/spec.md"])
            self.assertEqual(result, {"url":"https://example.test/projects/xnoq/design/spec.md", "files":1, "path":"projects/xnoq/design/spec.md"})
            with self.assertRaisesRegex(ValueError, "already exists"):
                client.publish(source, "projects/xnoq/design/spec.md")
            self.assertEqual(sum(c[0] == "PUT" for c in calls), 1)

    def test_exact_directory_and_validation(self):
        client, calls, stored = self.client()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "index.html").write_text("<h1>Hello</h1>")
            (root / "assets").mkdir()
            (root / "assets/app.js").write_text("console.log(1)")
            result = client.publish(root, "sites/my demo/")
            self.assertEqual(set(stored), {"sites/my demo/index.html", "sites/my demo/assets/app.js"})
            self.assertEqual(result['url'], "https://example.test/sites/my%20demo/")
            self.assertEqual(result['files'], 2)
            initial = len(calls)
            (root / ".env").write_text("secret")
            with self.assertRaises(ValueError):
                client.publish(root, "other-site")
            (root / ".env").unlink()
            (root / "linked").symlink_to(root / "index.html")
            with self.assertRaises(ValueError):
                client.publish(root, "other-site")
            self.assertEqual(len(calls), initial)

    def test_collision_preflight_is_complete(self):
        client, calls, stored = self.client({"project/z.md": b"existing"})
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "a.md").write_text("new")
            (root / "z.md").write_text("replacement")
            with self.assertRaisesRegex(ValueError, "already exists"):
                client.publish(root, "project")
            self.assertFalse(any(c[0] == "PUT" for c in calls))
            self.assertEqual(stored, {"project/z.md": b"existing"})

    def test_auth_failure_is_not_missing_file(self):
        client, _, _ = self.client()
        def denied(*args, **kwargs):
            raise dufs.DufsError(401)
        client.request = denied
        with tempfile.TemporaryDirectory() as temp:
            source = Path(temp) / "file.md"
            source.write_text("hello")
            with self.assertRaises(dufs.DufsError) as error:
                client.publish(source, "docs/file.md")
            self.assertEqual(error.exception.status, 401)


if __name__ == "__main__":
    unittest.main()
