"""Exercise source guards in disposable local repositories, never the real core."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("core_source", SOURCE / "scripts/verify-core-source.py")
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)


class SourceChecks(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "wing").mkdir()
        for path in ["compatibility/versions.json", "experiments/ifindex-shutdown-join.patch", "experiments/sniffer-lifetime.patch", "wing/go.mod"]:
            dest = self.root / path
            dest.parent.mkdir(exist_ok=True)
            shutil.copyfile(SOURCE / path, dest)
        self.run_git(self.root, "init", "-q")
        self.run_git(self.root, "config", "user.email", "fixture@example.invalid")
        self.run_git(self.root, "config", "user.name", "Synthetic fixture")
        self.run_git(self.root, "clone", "--quiet", "--shared", str(SOURCE / "wing/dae-core"), "wing/dae-core")
        self.core = self.root / "wing/dae-core"
        revision = checker.git(self.core, "rev-parse", "HEAD")
        self.run_git(self.root, "add", "compatibility", "experiments", "wing/go.mod")
        self.run_git(self.root, "update-index", "--add", "--cacheinfo", f"160000,{revision},wing/dae-core")
        self.run_git(self.root, "commit", "-qm", "synthetic source fixture")

    @staticmethod
    def run_git(root, *args):
        subprocess.run(["git", "-C", str(root), *args], check=True, capture_output=True)

    def change_manifest(self, edit):
        path = self.root / "compatibility/versions.json"
        value = json.loads(path.read_text())
        edit(value)
        path.write_text(json.dumps(value))

    def test_unmodified_profile_does_not_apply_patch(self):
        report = checker.verify(self.root)
        self.assertEqual(report["core_variant"], "unmodified-upstream")
        self.assertIsNone(report["applied_patch_sha256"])
        self.assertEqual(checker.git(self.core, "status", "--porcelain"), "")

    def test_patch_profile_is_explicit_and_reapplication_rejected(self):
        report = checker.verify(self.root, True)
        self.assertEqual(report["core_variant"], "private-lifecycle-fixes")
        self.assertEqual(checker.git(self.core, "diff", "--name-only"), "component/sniffing/sniffer.go\ncontrol/control_plane_core.go")
        with self.assertRaisesRegex(ValueError, "must be clean"):
            checker.verify(self.root, True)

    def test_wrong_gitlink_pin_rejected(self):
        self.change_manifest(lambda m: m["sources"]["dae"].update(commit="0" * 40))
        with self.assertRaisesRegex(ValueError, "gitlink"):
            checker.verify(self.root)

    def test_wrong_checked_out_revision_rejected(self):
        self.run_git(self.core, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "wrong revision")
        with self.assertRaisesRegex(ValueError, "Checked-out core revision"):
            checker.verify(self.root)

    def test_patch_tampering_is_rejected_without_mutation(self):
        path = self.root / "experiments/ifindex-shutdown-join.patch"
        path.write_text(path.read_text() + "\n")
        with self.assertRaisesRegex(ValueError, "checksum"):
            checker.verify(self.root, True)
        self.assertEqual(checker.git(self.core, "status", "--porcelain"), "")

    def test_patch_file_scope_rejected(self):
        self.change_manifest(lambda m: m["shutdown_patch"].update(files=["other.go"]))
        with self.assertRaisesRegex(ValueError, "reviewed scope"):
            checker.verify(self.root, True)

    def test_dependency_fork_mismatch_rejected(self):
        path = self.root / "wing/go.mod"
        path.write_text(path.read_text().replace("github.com/olicesx/outbound", "example.invalid/wrong"))
        with self.assertRaisesRegex(ValueError, "replacements differ"):
            checker.verify(self.root)


if __name__ == "__main__":
    unittest.main()
