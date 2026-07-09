"""Buttons: per-device Wake on LAN.

The only action in this integration. It rides the read-only token: the
router emits the magic packet itself and no SPR API is written.
"""

from __future__ import annotations

from homeassistant.components.button import ButtonEntity
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .api import SprApiError
from .coordinator import SprConfigEntry, SprCoordinator
from .entity import SprDeviceEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: SprConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    coordinator = entry.runtime_data
    known: set[str] = set()

    @callback
    def add_wake_buttons(macs: list[str] | None = None) -> None:
        new = [
            SprWakeButton(coordinator, mac)
            for mac in coordinator.data.devices
            if mac not in known
        ]
        known.update(button.mac for button in new)
        if new:
            async_add_entities(new)

    entry.async_on_unload(
        async_dispatcher_connect(hass, coordinator.signal_device_new, add_wake_buttons)
    )
    add_wake_buttons()


class SprWakeButton(SprDeviceEntity, ButtonEntity):
    """Send a Wake on LAN magic packet to this device.

    Disabled by default: most LAN clients can't be woken, so enable it just
    for the machines that support WoL.
    """

    _attr_translation_key = "wake_on_lan"
    _attr_entity_registry_enabled_default = False

    def __init__(self, coordinator: SprCoordinator, mac: str) -> None:
        super().__init__(coordinator, mac, "wake_on_lan")
        self.mac = mac

    async def async_press(self) -> None:
        try:
            await self.coordinator.api.wake_on_lan(self.mac)
        except SprApiError as err:
            raise HomeAssistantError(f"Wake on LAN failed: {err}") from err
