"""Test package init — pin the unit suite OVERLAY-HERMETIC.

Point INFRAWRIGHT_DEPLOYMENT at an empty deployment (overlay=".") so the suite
is unaffected by a committed deployment.json at the repo root — which an adopter
is *expected* to add per the overlay convention. Without this, the
tenant-materializing tests in test_lookup/test_transform redirect their output
under the overlay and fail, making "deployment.json present" and "tests pass"
mutually exclusive. setdefault, so an explicit override (e.g. deliberately
testing a real deployment) still wins.
"""
import os

os.environ.setdefault("INFRAWRIGHT_DEPLOYMENT", os.devnull)
