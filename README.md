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
python3 skills/dufs/scripts/dufs.py publish ./report.html
python3 skills/dufs/scripts/dufs.py publish ./dist
python3 skills/dufs/scripts/dufs.py list
python3 -m unittest discover -s tests
```

Each publication has a unique directory. No overwrites or delete operation. Multi-file uploads can be partial on failure; files do not expire automatically. Only upload a reviewed, clean export directory. HTML executes JavaScript: do not publish untrusted HTML on an authenticated writable origin.

## Docker server

The `deploy/compose.yaml` example binds only to loopback. Create a private `deploy/server.yaml` (mode 0600) from the example, set a strong password, and create `deploy/data`. Run `DUFS_UID=$(id -u) DUFS_GID=$(id -g) docker compose -f deploy/compose.yaml up -d` so the container can read your private config and write your data. Supply the same UID/GID for later Compose commands (or store these non-secret values in `deploy/.env`). Then expose it **only to your tailnet**, for example:

```sh
tailscale serve --bg --https=8443 http://127.0.0.1:5000
```

Choose an unused HTTPS port; do not reset existing Serve configuration. No Funnel is needed. All reads and writes require authentication. Browser previews prompt for the same username/password. Configure tailnet ACLs separately if only certain devices should reach the service.

The mounted data directory survives container replacement. Back up data and your encrypted credentials. For Nix/sops deployments, render the server auth config from sops rather than keeping a second plaintext source of truth.
