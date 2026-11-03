"""Offline packaging regression: fake compiler, real tar/zip and metadata."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ("darwin_arm64", "darwin_amd64", "linux_amd64", "linux_arm64", "windows_amd64")
VERSION = "test-rc"


class ReleaseArchiveTest(unittest.TestCase):
    def test_archives_contain_only_payload_and_licenses_without_host_metadata(self):
        with tempfile.TemporaryDirectory(prefix="release-fixture-") as directory:
            root = Path(directory)
            (root / "scripts").mkdir()
            (root / "tools").mkdir()
            for name in ("build.sh", "release.sh"):
                shutil.copy2(ROOT / "scripts" / name, root / "scripts" / name)
            for name in ("README.md", "LICENSE", "third_party/termenv/LICENSE"):
                dest = root / name
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_text("synthetic release fixture\n")
            if sys.platform == "darwin":
                subprocess.run(["xattr", "-w", "com.example.release-test", "private-metadata",
                                str(root / "README.md")], check=True)
            compiler = root / "tools/go"
            compiler.write_text(f"#!{sys.executable}\n" + """
import pathlib, sys
assert sys.argv[1] == 'build'
assert '-trimpath' in sys.argv
ldflags = sys.argv[sys.argv.index('-ldflags') + 1]
assert '-s' in ldflags and '-w' in ldflags
pathlib.Path(sys.argv[sys.argv.index('-o') + 1]).write_text('synthetic binary')
""")
            compiler.chmod(0o755)
            env = dict(os.environ, PATH=str(root / "tools") + os.pathsep + os.environ["PATH"],
                       VERSION=VERSION, REVISION="fixture", DIRTY="false",
                       SOURCE_DATE_EPOCH="1", DIST_DIR="dist",
                       # The fixture copies only the packaging scripts and uses
                       # a fake Go compiler, so skip the repository-level cache
                       # maintenance hook in this isolated test.
                       NIUMA_GO_CACHE_MAINTENANCE="0",
                       TARGETS=" ".join(t.replace("_", "/") for t in TARGETS), LC_ALL="C")
            # The test must challenge the script's own metadata defaults.
            env.pop("COPYFILE_DISABLE", None)
            env.pop("GOFLAGS", None)
            subprocess.run(["bash", str(root / "scripts/release.sh")], env=env, cwd=root,
                           check=True, capture_output=True, text=True)
            sums = dict(line.split()[::-1] for line in (root / "dist/SHA256SUMS").read_text().splitlines())
            self.assertEqual(len(sums), len(TARGETS))
            for target in TARGETS:
                stage = f"niuma_{VERSION}_{target}"
                is_windows = target.startswith("windows")
                binary = "niuma.exe" if is_windows else "niuma"
                archive = root / "dist" / (stage + (".zip" if is_windows else ".tar.gz"))
                self.assertEqual(hashlib.sha256(archive.read_bytes()).hexdigest(), sums[archive.name])
                expected = {f"{stage}/{name}" for name in
                            (binary, "README.md", "LICENSE", "THIRD_PARTY_LICENSES/termenv/LICENSE")}
                if is_windows:
                    with zipfile.ZipFile(archive) as zf:
                        self.assertEqual({i.filename for i in zf.infolist() if not i.is_dir()}, expected)
                        self.assertTrue(all(not i.extra for i in zf.infolist()))
                else:
                    with tarfile.open(archive) as tf:
                        self.assertEqual({m.name for m in tf if m.isfile()}, expected)
                        for member in tf.getmembers():
                            self.assertEqual((member.uid, member.gid), (0, 0))
                            self.assertIn(member.uname, ("", "root"))
                            self.assertIn(member.gname, ("", "root"))
                            self.assertFalse(member.pax_headers)
                            self.assertTrue(member.isfile() or member.isdir())


if __name__ == "__main__":
    unittest.main()
