"""Test package init — pin the unit suite OVERLAY-HERMETIC.

Point INFRAWRIGHT_DEPLOYMENT at an empty deployment (overlay=".") so the suite
is unaffected by a committed deployment.json at the repo root — which an adopter
is *expected* to add per the overlay convention. Without this, the
tenant-materializing tests in test_lookup/test_transform redirect their output
under the overlay and fail, making "deployment.json present" and "tests pass"
mutually exclusive. Unconditional (not setdefault): the Makefile now exports
INFRAWRIGHT_DEPLOYMENT for every engine invocation, so under `make test` a
setdefault would silently run the suite against the exported deployment and
diverge from a direct `python3 -m unittest` run. Tests that need a specific
deployment set the env var themselves in setUp, which still wins.
"""
import os

os.environ["INFRAWRIGHT_DEPLOYMENT"] = os.devnull
