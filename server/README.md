# Browser login gateway

Optional standard-library-only Go proxy in front of dufs. Browsers get a real HTML login form, not an HTTP authentication popup. Existing AI/WebDAV clients still send Authorization directly and dufs validates it.

- Login: `/__dufs/login`, username/password form with password-manager autocomplete.
- 90-day HMAC-signed sessions (key derived from the password) that survive gateway restarts and redeploys; Secure + HttpOnly + SameSite=Lax host-only cookie. Changing the password revokes every session. Logout revocation is kept in memory, so a copied cookie becomes valid again after a restart until it expires.
- Login and cookie-authenticated mutations enforce the configured Origin. Login attempts are bounded (20 per minute per peer address; a shared reverse proxy may share that quota).
- Built-in dufs `CHECKAUTH` and `LOGOUT` methods work; dufs's logout button clears the browser session. `POST /__dufs/logout` is also available.
- Native `WWW-Authenticate` challenges are suppressed, including invalid API credentials; API clients must send Authorization proactively (the bundled skill does).
- Streaming uploads, range downloads, WebDAV methods and HTML assets are proxied without buffering whole files.
- Browser page views of `.html` files and directory `index.html` pages get a small floating **Download** link (added in transit, files on disk are untouched; skipped for pages over 10 MB, the file-manager UI, iframes and API clients). Any file URL with `?download` returns the original bytes as an attachment.
- Create-only PUTs (`If-None-Match: *`) are checked against the authenticated backend and reject any existing file with 412, including empty files. PUTs are serialized through this single gateway so concurrent publishers cannot silently collide. This trades parallel upload throughput for safe exact-path publishing; it is not a distributed lock across separate gateways or host-side writers.
- Authenticated browser document navigation to `.md` redirects to the Material file browser and opens its full-screen Preview/Source reader. Fetch/API requests remain raw; `?raw=1` bypasses presentation explicitly. Other file operations and HTML navigation are unchanged.
- Passwords are never placed in URLs, logs, localStorage, sessionStorage or session cookies.

Configuration (all required except `LISTEN_ADDR`):

```text
PUBLIC_ORIGIN=https://files.example-tailnet.ts.net:8443
UPSTREAM=http://dufs:5000
DUFS_USERNAME=ai
DUFS_PASSWORD_FILE=/run/secrets/dufs_password
LISTEN_ADDR=:8080
```

The password file contains the same plaintext password the AI client uses. The dufs backend can keep its SHA-512 crypt hash. Mount both as read-only secret files, do not bake them into an image. Rotation requires updating both and recreating the containers.

Docker build from repository root:

```sh
docker build -f server/Dockerfile -t pi-dufs-login .
```

Put gateway and dufs in the same private Docker network. Publish only the gateway's port to host loopback, then point Tailscale Serve at it. Do not publish the backend. Preserve the original HTTPS Host header. The gateway accepts internal HTTP only because HTTPS terminates at Tailscale Serve.

Run tests: `go test -race ./server`.

`/__dufs/` is reserved. This is a trusted personal publishing service, not an untrusted HTML sandbox: scripts in published HTML share the authenticated origin and can act as the logged-in user. Do not publish untrusted HTML. A compromised local host/Docker daemon remains outside this boundary. No MFA, per-user accounts, distributed sessions, or public-internet deployment is provided.
