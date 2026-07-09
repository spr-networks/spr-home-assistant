"""Shared fixtures for the SPR integration tests."""

import os
import sys

import pytest

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, REPO_ROOT)

pytest_plugins = "pytest_homeassistant_custom_component"

# pytest-homeassistant-custom-component ships its own custom_components
# package (with __init__.py), which shadows the repo's namespace dir. Extend
# its search path so the loader finds custom_components/spr here too.
import custom_components  # noqa: E402

_ours = os.path.join(REPO_ROOT, "custom_components")
if _ours not in custom_components.__path__:
    custom_components.__path__.append(_ours)


@pytest.fixture(autouse=True)
def auto_enable_custom_integrations(enable_custom_integrations):
    """Allow loading custom_components in tests."""
    return
