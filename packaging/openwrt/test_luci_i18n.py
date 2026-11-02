#!/usr/bin/env python3
"""Tests for the LuCI translation catalog builder."""

from __future__ import annotations

import importlib.util
import os
import struct
import unittest

SCRIPT = os.path.join(os.path.dirname(__file__), "mkpackage.py")
SPEC = importlib.util.spec_from_file_location("mkpackage", SCRIPT)
mkpackage = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(mkpackage)


class LuCITranslationTests(unittest.TestCase):
    def test_po_translation_compiles_to_luci_lmo_catalog(self) -> None:
        source = '''msgid "Lanory Network Topology"\nmsgstr "Lanory 网络拓扑"\n'''

        catalog = mkpackage.compile_luci_po(source)

        key_id = mkpackage.luci_hash("Lanory Network Topology".encode())
        value = "Lanory 网络拓扑".encode()
        value_id = mkpackage.luci_hash(value)
        padded_value = value + bytes((-len(value)) % 4)
        index = struct.pack("!IIII", key_id, 1, 0, len(value))
        expected = padded_value + index + struct.pack("!I", len(padded_value))
        self.assertEqual(catalog, expected)

    def test_package_contains_translated_menu_catalog(self) -> None:
        package = mkpackage.build_luci_apk("0.1.1", os.path.dirname(__file__))
        try:
            with mkpackage.gzip.open(package, "rb") as compressed:
                with mkpackage.tarfile.open(fileobj=mkpackage.io.BytesIO(compressed.read())) as archive:
                    menu = archive.extractfile("usr/share/luci/menu.d/luci-app-lanory.json").read()
                    catalog = archive.extractfile("usr/lib/lua/luci/i18n/lanory.zh-cn.lmo").read()
            self.assertIn(b'"title": "Lanory Network Topology"', menu)
            self.assertTrue(catalog)
        finally:
            os.unlink(package)


if __name__ == "__main__":
    unittest.main()
