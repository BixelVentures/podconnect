import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('promotion', Path(__file__).parents[1] / 'scripts/native_image_promotion.py')
promotion = importlib.util.module_from_spec(spec)
spec.loader.exec_module(promotion)


class NativeImagePromotionTests(unittest.TestCase):
    def setUp(self):
        self.tree = 'a' * 40
        self.reference = 'ghcr.io/bixelventures/aarch64-podconnect:0.26.19'
        self.image = {'os': 'linux', 'architecture': 'arm64', 'config': {'Labels': {
            promotion.TREE_LABEL: self.tree, promotion.ARCH_LABEL: 'aarch64'}}}

    def decide(self, code=0, image=None, error=''):
        return promotion.decision(code, json.dumps(self.image if image is None else image), error,
            reference=self.reference, tree=self.tree, arch='aarch64')

    def test_exact_source_and_actual_architecture_reuse_without_build(self):
        self.assertEqual(self.decide(), 'reuse')

    def test_proven_missing_exact_tag_admits_initial_build(self):
        for reason in ('not found', 'manifest unknown'):
            self.assertEqual(self.decide(1, error=f'ERROR: {self.reference}: {reason}'), 'build')

    def test_existing_unlabelled_legacy_image_never_overwritten(self):
        self.image['config']['Labels'] = {}
        with self.assertRaises(ValueError): self.decide()

    def test_existing_different_source_never_overwritten(self):
        self.image['config']['Labels'][promotion.TREE_LABEL] = 'b' * 40
        with self.assertRaises(ValueError): self.decide()

    def test_actual_or_label_architecture_mismatch_rejects(self):
        self.image['architecture'] = 'amd64'
        with self.assertRaises(ValueError): self.decide()
        self.image['architecture'] = 'arm64'
        self.image['config']['Labels'][promotion.ARCH_LABEL] = 'amd64'
        with self.assertRaises(ValueError): self.decide()

    def test_inspect_uncertainty_and_other_tag_missing_reject(self):
        for message in ('unauthorized', 'network timeout', 'rate limited', 'server error',
                f'ERROR: {self.reference}: not found\nunauthorized',
                'ERROR: ghcr.io/another:0.26.19: not found'):
            with self.assertRaises(ValueError): self.decide(1, error=message)

    def test_malformed_or_index_metadata_rejects(self):
        for image in ({}, [], {'manifests': []}, {'os': 'windows'}):
            with self.assertRaises((ValueError, AttributeError)): self.decide(image=image)

    def test_workflow_only_builds_on_admission_and_labels_actual_subtree(self):
        root = Path(__file__).parents[1]
        workflow = (root / '.github/workflows/publish.yaml').read_text()
        script = (root / 'scripts/native_image_promotion.py').read_text()
        self.assertIn("if: steps.promotion.outputs.action == 'build'", workflow)
        self.assertIn('io.bixelventures.podconnect.source-tree=${{ steps.promotion.outputs.source_tree }}', workflow)
        self.assertIn("['git', 'rev-parse', 'HEAD:podconnect']", script)
        self.assertIn('timeout=60', script)
        self.assertIn('runner: ubuntu-24.04-arm', workflow)
        self.assertIn('provenance: false', workflow)
        self.assertIn('group: podconnect-speakers-${{ matrix.arch }}', workflow)
        self.assertIn('cancel-in-progress: false', workflow)
