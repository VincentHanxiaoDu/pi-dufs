#!/usr/bin/env python3
"""Apply small, checked adaptations to the pinned dufs Material assets release."""
import gzip
import json
from pathlib import Path
import sys
import tarfile
import zipfile

HERE = Path(__file__).resolve().parent


def replace_once(text, before, after):
    if text.count(before) != 1:
        raise ValueError(f"Unexpected upstream asset; expected one occurrence of {before!r}")
    return text.replace(before, after, 1)


def customize(archive, output, source_archive):
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(archive) as bundle:
        expected = {'index.html', 'index.css', 'index.js', 'vite.svg'}
        if set(bundle.namelist()) != expected:
            raise ValueError('Unexpected release file list')
        html = bundle.read('index.html').decode()
        js = bundle.read('index.js').decode()
        css = bundle.read('index.css').decode()
    html = replace_once(html, '<html>', '<html lang="en">')
    html = replace_once(html, 'width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no', 'width=device-width,initial-scale=1')
    html = replace_once(html, '__ASSETS_PREFIX__favicon.ico', '__ASSETS_PREFIX__favicon.svg')
    # Keep document contents, including rendered Markdown math, off external services.
    policy = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' data: blob:; font-src 'self' data: blob:; connect-src 'self'; frame-src 'self' blob:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; form-action 'self'"
    config = {
        'document': '${path} · Private Files',
        'page': {'title': 'Private Files', 'logo': '__ASSETS_PREFIX__favicon.svg'},
        'footer': 'Tailscale only · [Theme source (AGPL-3.0)](__ASSETS_PREFIX__theme-source.tar.gz)',
        'theme': {'light': {'primary': '#4169c1'}, 'dark': {'primary': '#a2c7ff'}},
        'externalViewer': False,
        'jxlPolyfill': False,
        'preferPreview': False,
        'limit': 100,
    }
    html = replace_once(html, '<head>', '<head><meta http-equiv="Content-Security-Policy" content="'+policy+'"><script>window.__DUFS_MATERIAL_CONFIG__='+json.dumps(config, separators=(',', ':'))+';</script>')
    js = replace_once(js, 'locale:navigator.language', 'locale:"en"')
    # Render Markdown on click without turning HTML website links into code previews.
    js = replace_once(js, 'let st=(e,t)=>{d&&(', 'let st=(e,t)=>{(d||e.ext==="md")&&(')
    # Reuse the existing Markdown fetch/parser; source is rendered as escaped text, never HTML.
    js = replace_once(js, 'ae=R(``),oe=R(!1)', 'ae=R(``),dufsMdSource=R(""),dufsMdMode=R("preview"),oe=R(!1)')
    js = replace_once(js, 'me=async()=>{if(!F.value.fullpath)return;let e=', 'me=async()=>{if(!F.value.fullpath)return;const dufsMdPath=F.value.fullpath;dufsMdSource.value="";dufsMdMode.value="preview";let e=')
    js = replace_once(js, 't=await a(F.value.fullpath).then(e=>e.text());if(v.value=', 't=await a(dufsMdPath).then(e=>e.text());if(F.value.fullpath!==dufsMdPath){clearTimeout(e);return}dufsMdSource.value=t;if(v.value=')
    switches = 'F.value.ext==="md"?q("div",{class:"dufs-markdown-modes",role:"group","aria-label":"Markdown view"},[q("button",{type:"button","aria-pressed":dufsMdMode.value==="preview",onClick:()=>dufsMdMode.value="preview"},"Preview",8,["aria-pressed"]),q("button",{type:"button","aria-pressed":dufsMdMode.value==="source",onClick:()=>dufsMdMode.value="source"},"Source",8,["aria-pressed"])]):Ps("",true)'
    js = replace_once(js, '}),J(yA),P.value===`image`?', '}),'+switches+',J(yA),P.value===`image`?')
    source_view = 'F.value.ext==="md"&&dufsMdMode.value==="source"?q("pre",{class:"dufs-markdown-source",tabindex:0,"aria-label":"Markdown source"},[q("code",null,je(dufsMdSource.value),1)]):'
    js = replace_once(js, 'default:U(()=>[F.value.ext===`md`||B(pE)[F.value.ext]?', 'default:U(()=>['+source_view+'F.value.ext===`md`||B(pE)[F.value.ext]?')
    # Empty q selects dufs directory listing even when render-try-index is enabled.
    js = replace_once(js, 'to:e.fullpath+`/`', 'to:e.fullpath+`/?q=`')
    js = replace_once(js, 'title:decodeURIComponent(n),href:t}', 'title:decodeURIComponent(n),href:t+"?q="}')
    js = replace_once(js, 'new URLSearchParams([[`json`,``]]);b.value&&t.append', 'new URLSearchParams([[`json`,``],[`q`,``]]);b.value&&t.set')
    # The stock theme refreshes the list after LOGOUT; our cookie gateway needs navigation.
    js = replace_once(js, 'n.open(`LOGOUT`,`${M.value}`,!0,C.value.user),n.send()}),await ne())', 'n.open(`LOGOUT`,`${M.value}`,!0,C.value.user),n.send()}),location.assign("/__dufs/login"))')
    (output / 'index.html').write_text(html)
    (output / 'index.js').write_text(js)
    (output / 'index.css').write_text(css + '\n' + (HERE / 'mobile.css').read_text())
    (output / 'favicon.svg').write_text('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48"><rect x="4" y="10" width="40" height="32" rx="6" fill="#4169c1"/><path d="M4 15V10a4 4 0 0 1 4-4h12l6 9" fill="#a2c7ff"/><path d="M12 25h24M12 32h16" stroke="white" stroke-width="3" stroke-linecap="round"/></svg>')
    # Offer the exact upstream sources and our adaptations to every UI user.
    def normalized(info):
        info.uid = info.gid = 0
        info.uname = info.gname = ''
        info.mtime = 1
        return info

    with (output / 'theme-source.tar.gz').open('wb') as raw:
        with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w') as sources:
                sources.add(source_archive, arcname='upstream-source-v0.47.0.tar.gz', filter=normalized)
                for name in ('customize.py', 'mobile.css', 'README.md', 'LICENSE'):
                    sources.add(HERE / name, arcname=name, filter=normalized)


if __name__ == '__main__':
    if len(sys.argv) != 4:
        raise SystemExit('Usage: customize.py RELEASE.zip OUTPUT_DIR UPSTREAM_SOURCE.tar.gz')
    customize(*sys.argv[1:])
