# pi-dufs

A small Pi skill for publishing files and static HTML sites to your own [dufs](https://github.com/sigoden/dufs) server. No extension runtime or third-party Python dependencies. Other AI clients can use the same skill and CLI.

## Install

```sh
pi install git:https://github.com/VincentHanxiaoDu/pi-dufs.git
```

Reload Pi, then ask it to publish an HTML preview or use `/skill:dufs`.

Requires Python 3.9+. Configure `DUFS_URL`, `DUFS_USERNAME` and `DUFS_PASSWORD_FILE` (or `DUFS_PASSWORD`). Alternatively create `~/.config/pi-dufs/config.json`:

```json
{"url":"https://your-server.your-tailnet.ts.net:8443","username":"ai","passwordFile":"/path/to/sops/secret"}
```

The config-file fallback also works in long-lived AI services that do not inherit an interactive shell's environment. Never commit passwords. Use HTTPS; the client refuses redirects and bypasses ambient HTTP proxies to avoid leaking private traffic.

```sh
python3 skills/dufs/scripts/dufs.py check
python3 skills/dufs/scripts/dufs.py publish ./report.html reports/analysis.html
python3 skills/dufs/scripts/dufs.py publish ./spec.md projects/example/design/spec.md
python3 skills/dufs/scripts/dufs.py publish ./dist sites/example
python3 skills/dufs/scripts/dufs.py list
python3 -m unittest discover -s tests
```

The caller chooses the **exact remote path**. No UUID, timestamp, prefix, suffix or wrapper directory is added. A file destination includes its filename; a directory destination receives the source directory's contents. `publish` now requires this destination argument; older one-argument commands fail instead of silently inventing names. Existing directories and share URLs are not migrated.

All target files are checked before uploading. Existing files fail instead of being renamed or overwritten. The optional gateway enforces `If-None-Match: *` and serializes PUT requests to prevent concurrent creation races, including empty files; standalone dufs preflight is not atomic. There is no overwrite or delete command. Multi-file uploads can be partial on failure; inspect the chosen location before retrying. Files do not expire automatically. Only upload a reviewed, clean export directory. HTML executes JavaScript: do not publish untrusted HTML on an authenticated writable origin.

## Docker server

The `deploy/compose.yaml` example binds only to loopback. Create a private `deploy/server.yaml` (mode 0600) from the example, set a strong password, and create `deploy/data`. Run `DUFS_UID=$(id -u) DUFS_GID=$(id -g) docker compose -f deploy/compose.yaml up -d` so the container can read your private config and write your data. Supply the same UID/GID for later Compose commands (or store these non-secret values in `deploy/.env`). Then expose it **only to your tailnet**, for example:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:5000
```

Choose an unused HTTPS port; do not reset existing Serve configuration. No Funnel is needed. All reads and writes require authentication. Without the optional gateway, browser previews use the browser's native authentication popup. For a mobile-friendly HTML login page with a 90-day session that survives restarts, deploy the [login gateway](server/README.md) in front of dufs; it uses the same username/password and preserves the AI client's API. Configure tailnet ACLs separately if only certain devices should reach the service.

For a Material file browser with English labels, breadcrumbs, search and mobile-friendly navigation, see the optional [Material UI adaptations](themes/material/README.md). The theme is AGPL-3.0-only; its source bundle is linked from the file browser. The skill and login gateway remain MIT licensed.

The mounted data directory survives container replacement. Back up data and your encrypted credentials. For Nix/sops deployments, render the server auth config from sops rather than keeping a second plaintext source of truth.
