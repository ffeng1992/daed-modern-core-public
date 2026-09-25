import importlib.util
import json
import pathlib
import tempfile
import unittest

path = pathlib.Path(__file__).with_name('check-acceptance-results.py')
spec = importlib.util.spec_from_file_location('acceptance_results', path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ResultChecks(unittest.TestCase):
    def test_kernel_exact_pass_and_failure_controls(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            binary = root / 'dae-preparation.test'
            log = root / 'test.log'
            binary.write_bytes(b'test fixture')
            lines = [f'--- PASS: {name} (0.01s)' for name in module.EXPECTED_KERNEL] + ['PASS']
            log.write_text('\n'.join(lines) + '\n')
            self.assertEqual(module.check_kernel(log, binary)['status'], 'PASS')
            binary.unlink()
            with self.assertRaisesRegex(ValueError, 'artifact missing'):
                module.check_kernel(log, binary)
            binary.write_bytes(b'test fixture')
            for altered, reason in (
                (lines[:-2] + ['PASS'], 'TestIsolatedGraphQLRuntime'),
                ([x.replace('PASS', 'SKIP') if 'LANForwarding' in x else x for x in lines], 'LANForwarding'),
                ([x.replace('PASS', 'FAIL') if 'GraphQLRuntime' in x else x for x in lines], 'GraphQLRuntime'),
                (lines[:-1], 'suite did not finish'),
            ):
                log.write_text('\n'.join(altered) + '\n')
                with self.assertRaisesRegex(ValueError, reason):
                    module.check_kernel(log, binary)

    def test_full_report_rejects_missing_and_skipped_checks(self):
        with tempfile.TemporaryDirectory() as tmp:
            report_path = pathlib.Path(tmp) / 'report.json'
            report = {
                'product_source_sha': 'e7347b2becc4b4a82adda0b23ce3c9d0621cd5ea',
                'runtime_image_origin': 'Dockerfile+docker-compose.yml',
                'runtime_image_id': 'sha256:' + 'a' * 64,
                'guest_kernel': 'fixture-kernel', 'docker_version': 'fixture', 'compose_version': 'fixture',
                'stages': {stage: {'status': 'PASS', 'checks': {
                    name: {'status': 'PASS', 'evidence': f'evidence/{stage}/{name}.json'} for name in names
                }} for stage, names in module.REQUIRED_STAGES.items()},
                'official_daed_comparison': {'status': 'NOT_RUN', 'reason': 'no verified binary'},
            }
            report_path.write_text(json.dumps(report))
            for stage, checks in module.REQUIRED_STAGES.items():
                for name in checks:
                    evidence = pathlib.Path(tmp) / f'evidence/{stage}/{name}.json'
                    evidence.parent.mkdir(parents=True, exist_ok=True)
                    evidence.write_text('{"fixture":true}')
            self.assertEqual(module.check_full(report_path)['candidate'], 'PASS')
            del report['stages']['C']['checks']['proxy_udp']
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, 'proxy_udp'):
                module.check_full(report_path)
            report['stages']['C']['checks']['proxy_udp'] = {'status': 'SKIP', 'evidence': 'fixture'}
            report_path.write_text(json.dumps(report))
            with self.assertRaisesRegex(ValueError, 'lacks PASS'):
                module.check_full(report_path)


if __name__ == '__main__':
    unittest.main()
