"""The SPR (Secure Programmable Router) integration."""

from __future__ import annotations

import voluptuous as vol

from homeassistant.const import CONF_HOST, CONF_PORT, CONF_TOKEN, Platform
from homeassistant.core import HomeAssistant, ServiceCall
from homeassistant.helpers import config_validation as cv
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.device_registry import format_mac
from homeassistant.helpers.typing import ConfigType
from homeassistant.exceptions import HomeAssistantError

from .api import SprApiClient, SprApiError
from .const import DOMAIN
from .coordinator import SprConfigEntry, SprCoordinator

PLATFORMS: list[Platform] = [
    Platform.BINARY_SENSOR,
    Platform.BUTTON,
    Platform.DEVICE_TRACKER,
    Platform.SENSOR,
    Platform.SWITCH,
    Platform.UPDATE,
]

CONFIG_SCHEMA = cv.config_entry_only_config_schema(DOMAIN)

SERVICE_WAKE_ON_LAN = "wake_on_lan"
SERVICE_WAKE_SCHEMA = vol.Schema({vol.Required("mac"): cv.string})


async def async_setup(hass: HomeAssistant, config: ConfigType) -> bool:
    """Register domain services (available even with entries unloaded)."""

    async def handle_wake(call: ServiceCall) -> None:
        mac = format_mac(call.data["mac"])
        entries: list[SprConfigEntry] = hass.config_entries.async_loaded_entries(DOMAIN)
        if not entries:
            raise HomeAssistantError("No SPR router is configured")

        # Prefer the router that actually knows this device, so a magic
        # packet lands in the right broadcast domain on multi-router setups.
        owning = [e for e in entries if mac in e.runtime_data.data.devices]
        ordered = owning or entries

        errors = []
        for entry in ordered:
            try:
                await entry.runtime_data.api.wake_on_lan(mac)
                return
            except SprApiError as err:
                errors.append(str(err))
        raise HomeAssistantError(f"Wake on LAN failed: {'; '.join(errors)}")

    hass.services.async_register(
        DOMAIN, SERVICE_WAKE_ON_LAN, handle_wake, schema=SERVICE_WAKE_SCHEMA
    )
    return True


async def async_setup_entry(hass: HomeAssistant, entry: SprConfigEntry) -> bool:
    """Set up SPR from a config entry."""
    api = SprApiClient(
        async_get_clientsession(hass),
        entry.data[CONF_HOST],
        entry.data[CONF_PORT],
        entry.data[CONF_TOKEN],
    )
    coordinator = SprCoordinator(hass, entry, api)
    await coordinator.async_config_entry_first_refresh()
    entry.runtime_data = coordinator

    await hass.config_entries.async_forward_entry_setups(entry, PLATFORMS)
    coordinator.start_event_listener()
    return True


async def async_unload_entry(hass: HomeAssistant, entry: SprConfigEntry) -> bool:
    """Unload a config entry."""
    await entry.runtime_data.stop_event_listener()
    return await hass.config_entries.async_unload_platforms(entry, PLATFORMS)
