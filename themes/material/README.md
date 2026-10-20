# Material UI adaptations

Based on [TransparentLC/dufs-material-assets v0.47.0](https://github.com/TransparentLC/dufs-material-assets/tree/v0.47.0), commit `d7351c3`. This directory's theme adaptations are licensed under **AGPL-3.0-only**, matching the upstream UI; see LICENSE. The separate login gateway and publishing skill remain MIT licensed.

The checked customization script uses the official prebuilt release, not an unpinned frontend build. It forces English, sets a local logo and title, keeps pinch-to-zoom enabled, and makes the mobile file table show names/actions without excessive horizontal scrolling. Desktop retains size and modification time. Existing breadcrumbs, search, sorting, previews and logout come from upstream.

Third-party viewers and JXL CDN loading are disabled. The directory UI's CSP blocks external connections and images, including external Markdown math rendering. Directly served user HTML is not modified. No analytics or external fonts are added.

Reproduce from the public repository (Python 3 only):

```sh
curl -fL https://github.com/TransparentLC/dufs-material-assets/releases/download/v0.47.0/dufs-material-assets-embed.zip -o release.zip
curl -fL https://github.com/TransparentLC/dufs-material-assets/archive/refs/tags/v0.47.0.tar.gz -o upstream-source.tar.gz
python3 themes/material/customize.py release.zip assets upstream-source.tar.gz
```

When using the downloadable source bundle, `customize.py`, `mobile.css`, LICENSE and this README are at the root; run `python3 customize.py release.zip assets upstream-source-v0.47.0.tar.gz` instead. The bundle contains the upstream source tarball unchanged. Refer to the upstream README/package scripts to build the original release from source.

Point dufs's `assets` option at the generated directory. Keep the `theme-source.tar.gz` output: the UI footer links to it to provide the corresponding sources and modifications to every user. Retain upstream credits.

The existing login gateway handles CHECKAUTH and LOGOUT. A checked bundle patch redirects to its HTML login page after logout instead of refreshing an unauthorized file list. Token-only links for external downloaders still require gateway authentication; this adaptation does not loosen access controls. Only host trusted HTML/Markdown, as with the default dufs UI.
