import importlib.util
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).parents[1]
spec = importlib.util.spec_from_file_location('material', ROOT / 'themes/material/customize.py')
material = importlib.util.module_from_spec(spec)
spec.loader.exec_module(material)


class MaterialTests(unittest.TestCase):
    def test_checked_assets_and_source_offer(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            release = root / 'release.zip'
            with zipfile.ZipFile(release, 'w') as bundle:
                bundle.writestr('index.html', '<html><head><meta content="width=device-width,initial-scale=1,maximum-scale=1,user-scalable=no" name="viewport"><link href="__ASSETS_PREFIX__favicon.ico"></head><body>__INDEX_DATA__</body></html>')
                bundle.writestr('index.js', 'createI18n({locale:navigator.language}); let st=(e,t)=>{d&&(; to:e.fullpath+`/`; title:decodeURIComponent(n),href:t}; new URLSearchParams([[`json`,``]]);b.value&&t.append; n.open(`LOGOUT`,`${M.value}`,!0,C.value.user),n.send()}),await ne())')
                bundle.writestr('index.css', 'body{margin:0}')
                bundle.writestr('vite.svg', '<svg/>')
            source = root / 'source.tar.gz'
            source.write_bytes(b'source-fixture')
            output = root / 'assets'
            material.customize(release, output, source)
            html = (output / 'index.html').read_text()
            self.assertIn('<html lang="en">', html)
            self.assertIn('Content-Security-Policy', html)
            self.assertIn("connect-src 'self'", html)
            self.assertIn('"externalViewer":false', html)
            self.assertIn('"jxlPolyfill":false', html)
            self.assertIn('__INDEX_DATA__', html)
            self.assertNotIn('user-scalable=no', html)
            self.assertNotIn('navigator.language', (output / 'index.js').read_text())
            js = (output / 'index.js').read_text()
            self.assertIn('location.assign("/__dufs/login")', js)
            self.assertIn('let st=(e,t)=>{(d||e.ext==="md")&&(', js)
            self.assertIn('"preferPreview":false', html)
            self.assertIn('to:e.fullpath+`/?q=`', js)
            self.assertIn('href:t+"?q="', js)
            self.assertIn('[[`json`,``],[`q`,``]]', js)
            self.assertNotIn('b.value&&t.append', js)
            self.assertTrue((output / 'favicon.svg').is_file())
            material.customize(release, root / 'second-build', source)
            self.assertEqual((output / 'theme-source.tar.gz').read_bytes(), (root / 'second-build/theme-source.tar.gz').read_bytes())
            with tarfile.open(output / 'theme-source.tar.gz') as archive:
                self.assertEqual(set(archive.getnames()), {'upstream-source-v0.47.0.tar.gz', 'customize.py', 'mobile.css', 'README.md', 'LICENSE'})

    def test_upstream_drift_fails(self):
        for text in ('no match', 'old old'):
            with self.assertRaises(ValueError):
                material.replace_once(text, 'old', 'new')


if __name__ == '__main__':
    unittest.main()
