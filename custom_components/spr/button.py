"""Buttons: restart SPR services, per-device Wake on LAN."""

from __future__ import annotations

from homeassistant.components.button import ButtonDeviceClass, ButtonEntity
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .api import SprApiError
from .coordinator import SprConfigEntry, SprCoordinator
from .entity import SprDeviceEntity, SprRouterEntity


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

    async_add_entities([SprRestartButton(coordinator)])
    entry.async_on_unload(
        async_dispatcher_connect(hass, coordinator.signal_device_new, add_wake_buttons)
    )
    add_wake_buttons()


class SprRestartButton(SprRouterEntity, ButtonEntity):
    """Restart the SPR service containers."""

    _attr_device_class = ButtonDeviceClass.RESTART
    _attr_translation_key = "restart"

    def __init__(self, coordinator: SprCoordinator) -> None:
        super().__init__(coordinator, "restart")

    async def async_press(self) -> None:
        try:
            await self.coordinator.api.restart_router()
        except SprApiError as err:
            raise HomeAssistantError(f"Restart failed: {err}") from err


class SprWakeButton(SprDeviceEntity, ButtonEntity):
    """Send a Wake on LAN magic packet to this device."""

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
