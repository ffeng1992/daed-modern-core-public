import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location('contract', Path(__file__).with_name('check-config-contract.py'))
contract = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(contract)


class ConfigContractTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ('wing/dae-core/config/config.go', 'apps/web/src/components/ConfigFormModal.tsx', 'compatibility/config-contract.json'):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(contract.ROOT / name, target)

    def edit(self, file, old, new):
        path = self.root / file
        text = path.read_text()
        self.assertIn(old, text)
        path.write_text(text.replace(old, new, 1))

    def test_reviewed_pair(self):
        self.assertEqual(contract.check(self.root)['result'], 'PASS')

    def test_core_default_change(self):
        self.edit('wing/dae-core/config/config.go', 'default:"262144"', 'default:"131072"')
        with self.assertRaisesRegex(ValueError, 'contract changed'):
            contract.check(self.root)

    def test_new_core_field(self):
        self.edit('wing/dae-core/config/config.go', 'type Global struct {', 'type Global struct {\n NewFlag bool `mapstructure:"new_flag" default:"true"`')
        with self.assertRaisesRegex(ValueError, 'contract changed'):
            contract.check(self.root)

    def test_daed_only_change(self):
        self.edit('apps/web/src/components/ConfigFormModal.tsx', 'dialMode: z.string(),', 'dialMode: z.string().min(1),')
        with self.assertRaisesRegex(ValueError, 'contract changed'):
            contract.check(self.root)

    def test_both_change(self):
        self.edit('wing/dae-core/config/config.go', 'type Global struct {', 'type Global struct {\n NewFlag bool `mapstructure:"new_flag"`')
        self.edit('apps/web/src/components/ConfigFormModal.tsx', 'dialMode: z.string(),', 'dialMode: z.string().min(1),')
        with self.assertRaisesRegex(ValueError, 'contract changed'):
            contract.check(self.root)

    def test_parser_drift_fails_closed(self):
        self.edit('wing/dae-core/config/config.go', 'type Global struct {', 'type Global = struct {')
        with self.assertRaisesRegex(ValueError, 'Cannot locate'):
            contract.check(self.root)
