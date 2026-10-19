#!/usr/bin/env python3
"""Dependency-free, authenticated dufs publishing client."""
import argparse
import base64
import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError
from urllib.parse import quote, urlsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler, ProxyHandler
import uuid


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("Refusing redirect: credentials must stay on the configured server")


def settings():
    config_path = Path(os.environ.get("DUFS_CONFIG", "~/.config/pi-dufs/config.json")).expanduser()
    config = json.loads(config_path.read_text()) if config_path.exists() else {}
    url = os.environ.get("DUFS_URL", config.get("url", "")).rstrip("/")
    parsed = urlsplit(url)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("DUFS_URL must be an HTTPS URL without credentials, query or fragment")
    user = os.environ.get("DUFS_USERNAME", config.get("username", "ai"))
    password = os.environ.get("DUFS_PASSWORD")
    if not password:
        path = os.environ.get("DUFS_PASSWORD_FILE", config.get("passwordFile", ""))
        if not path:
            raise ValueError("Set DUFS_PASSWORD_FILE (recommended) or DUFS_PASSWORD")
        password = Path(path).expanduser().read_text().strip()
    if not password or ":" in user or "\n" in user:
        raise ValueError("Invalid credentials")
    return url, base64.b64encode(f"{user}:{password}".encode()).decode()


def remote_path(value):
    if not value or value.startswith("/") or "\\" in value or any(p in ("", ".", "..") for p in value.split("/")):
        raise ValueError("Remote path must be relative, with no empty, dot or parent components")
    return quote(value, safe="/")


class Client:
    def __init__(self):
        self.url, self.auth = settings()
        # Private service traffic must not pass through ambient HTTP proxies.
        self.opener = build_opener(ProxyHandler({}), NoRedirect())

    def request(self, method, path="", data=None, size=None, query=""):
        url = self.url + "/" + (remote_path(path) if path else "") + query
        headers = {"Authorization": "Basic " + self.auth}
        if size is not None:
            headers["Content-Length"] = str(size)
            headers["Content-Type"] = "application/octet-stream"
        try:
            with self.opener.open(Request(url, data=data, headers=headers, method=method), timeout=60) as response:
                return response.read(1024 * 1024)
        except HTTPError as error:
            # Never include credential-bearing headers or untrusted response bodies.
            raise ValueError(f"dufs returned HTTP {error.code}") from None

    def publish(self, source):
        source = Path(source).expanduser()
        if source.is_symlink() or not source.exists():
            raise ValueError("Source must exist and must not be a symlink")
        files = sorted(source.rglob("*")) if source.is_dir() else [source]
        for file in files:
            if file.is_symlink() or not (file.is_file() or file.is_dir()):
                raise ValueError("Symlinks and special files are not published")
            if file.is_file():
                relative = file.relative_to(source) if source.is_dir() else Path(file.name)
                if any(p.startswith(".") for p in relative.parts):
                    raise ValueError("Hidden files are not published; prepare a clean export directory")
        files = [f for f in files if f.is_file()]
        if not files:
            raise ValueError("No files to publish")
        prefix = uuid.uuid4().hex
        for file in files:
            relative = file.relative_to(source).as_posix() if source.is_dir() else file.name
            with file.open("rb") as stream:
                self.request("PUT", prefix + "/" + relative, stream, file.stat().st_size)
        target = prefix + "/" if source.is_dir() else prefix + "/" + quote(source.name, safe="")
        self.request("HEAD", prefix + ("/index.html" if source.is_dir() and (source / "index.html").is_file() else "/" + (files[0].relative_to(source).as_posix() if source.is_dir() else source.name)))
        return {"url": self.url + "/" + target, "files": len(files)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    publish = commands.add_parser("publish", help="Upload a file or clean static-site directory into a unique folder")
    publish.add_argument("source")
    listing = commands.add_parser("list", help="List a server directory as JSON")
    listing.add_argument("path", nargs="?", default="")
    commands.add_parser("check", help="Check authenticated server access")
    args = parser.parse_args()
    try:
        client = Client()
        if args.command == "publish":
            print(json.dumps(client.publish(args.source)))
        elif args.command == "list":
            print(client.request("GET", args.path, query="?json").decode())
        else:
            client.request("HEAD")
            print(json.dumps({"ok": True, "url": client.url}))
    except (ValueError, OSError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
