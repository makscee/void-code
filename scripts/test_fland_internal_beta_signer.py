#!/usr/bin/env python3
import hashlib
import importlib.util
import os
import pathlib
import tempfile
import unittest

MODULE_PATH = pathlib.Path(__file__).with_name('fland-internal-beta-signer.py')
SPEC = importlib.util.spec_from_file_location('fland_internal_beta_signer', MODULE_PATH)
signer = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(signer)


def payload(**overrides):
    version = '0.1.3-beta.5'
    filename = f'Void-Code-{version}-windows-x64.exe'
    value = {
        'schema': 'vc-windows-update-v1', 'channel': 'closed-beta', 'keyId': signer.KEY_ID,
        'version': version, 'platform': 'win32', 'architecture': 'x64', 'sequence': 5,
        'installerUrl': f'https://vc.makscee.ru/download/windows/{filename}',
        'immutableUrl': f'https://github.com/makscee/void-code/releases/download/desktop-v{version}/{filename}',
        'size': 1, 'sha256': 'a' * 64, 'sha512': 'AQ==' * 0 + 'A' * 86 + '==',
        'publishedAt': '2099-01-01T00:00:00.000Z', 'notBefore': '2099-01-01T00:00:00.000Z',
        'expiresAt': '2099-01-08T00:00:00.000Z',
    }
    value.update(overrides)
    return value


class FixedSignerPolicyTests(unittest.TestCase):
    def test_numeric_boundaries_match_runtime_and_builder(self):
        for size in (1, 2_147_483_648):
            signer.validate_payload(payload(size=size))
        for size in (True, False, 0, 2_147_483_649, 1.5):
            with self.subTest(size=size), self.assertRaises(ValueError):
                signer.validate_payload(payload(size=size))
        for sequence in (True, 4, 6, 1.5, signer.MAX_SAFE_INTEGER + 1):
            with self.subTest(sequence=sequence), self.assertRaises(ValueError):
                signer.validate_payload(payload(sequence=sequence))

    def test_time_policy_rejects_noncanonical_reversed_overlong_and_expired_windows(self):
        cases = [
            {'publishedAt': '2099-01-01T00:00:00Z'},
            {'publishedAt': '2099-01-02T00:00:00.000Z'},
            {'expiresAt': '2099-01-08T00:00:00.001Z'},
            {
                'publishedAt': '2020-01-01T00:00:00.000Z',
                'notBefore': '2020-01-01T00:00:00.000Z',
                'expiresAt': '2020-01-08T00:00:00.000Z',
            },
        ]
        for change in cases:
            with self.subTest(change=change), self.assertRaises(ValueError):
                signer.validate_payload(payload(**change))

    def test_operation_binding_survives_restart_and_rejects_equivocation(self):
        self.assertTrue(signer.OPERATION_BINDING.endswith('/used/beta.5-sequence-5.payload-sha256'))
        first = hashlib.sha256(b'exact beta.5 payload').hexdigest()
        second = hashlib.sha256(b'distinct beta.5 payload').hexdigest()
        with tempfile.TemporaryDirectory() as directory:
            binding = os.path.join(directory, 'beta.5-sequence-5.payload-sha256')
            signer.bind_operation(binding, first)
            self.assertEqual(pathlib.Path(binding).read_bytes(), (first + '\n').encode())
            # A restarted signer may reproduce the deterministic result for identical bytes.
            signer.bind_operation(binding, first)
            with self.assertRaisesRegex(ValueError, 'operation payload mismatch'):
                signer.bind_operation(binding, second)
            self.assertEqual(pathlib.Path(binding).read_bytes(), (first + '\n').encode())


if __name__ == '__main__':
    unittest.main()
