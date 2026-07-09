"""The SPR (Secure Programmable Router) integration.

Read-only: presence, telemetry, and update info. All write operations stay
in the SPR UI; the token this integration holds cannot mutate router state.
"""

from __future__ import annotations

from homeassistant.const import CONF_TOKEN, CONF_URL, CONF_VERIFY_SSL, Platform
from homeassistant.core import HomeAssistant
from homeassistant.helpers.aiohttp_client import async_get_clientsession

from .api import SprApiClient
from .const import DEFAULT_VERIFY_SSL
from .coordinator import SprConfigEntry, SprCoordinator

PLATFORMS: list[Platform] = [
    Platform.BINARY_SENSOR,
    Platform.DEVICE_TRACKER,
    Platform.SENSOR,
    Platform.UPDATE,
]


async def async_setup_entry(hass: HomeAssistant, entry: SprConfigEntry) -> bool:
    """Set up SPR from a config entry."""
    session = async_get_clientsession(
        hass, verify_ssl=entry.data.get(CONF_VERIFY_SSL, DEFAULT_VERIFY_SSL)
    )
    api = SprApiClient(session, entry.data[CONF_URL], entry.data[CONF_TOKEN])
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
