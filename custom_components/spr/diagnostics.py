"""Diagnostics for the SPR integration."""

from __future__ import annotations

from dataclasses import asdict
from typing import Any

from homeassistant.components.diagnostics import async_redact_data
from homeassistant.const import CONF_TOKEN
from homeassistant.core import HomeAssistant

from .coordinator import SprConfigEntry

TO_REDACT = [CONF_TOKEN, "mac", "ip", "wan_ip", "name"]


async def async_get_config_entry_diagnostics(
    hass: HomeAssistant, entry: SprConfigEntry
) -> dict[str, Any]:
    coordinator = entry.runtime_data
    return {
        "entry": async_redact_data(dict(entry.data), TO_REDACT),
        "options": dict(entry.options),
        "router": async_redact_data(coordinator.data.router, TO_REDACT),
        "traffic": coordinator.data.traffic,
        "devices": [
            async_redact_data(asdict(device), TO_REDACT)
            for device in coordinator.data.devices.values()
        ],
    }
