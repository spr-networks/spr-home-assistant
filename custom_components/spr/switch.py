"""Switches: guest wifi, per-device internet blocking (parental controls)."""

from __future__ import annotations

from typing import Any

from homeassistant.components.switch import SwitchEntity
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

    entities: list[SwitchEntity] = [SprGuestWifiSwitch(coordinator)]
    known: set[str] = set()

    @callback
    def add_block_switches(macs: list[str] | None = None) -> None:
        new = [
            SprBlockSwitch(coordinator, mac)
            for mac in coordinator.data.devices
            if mac not in known
        ]
        known.update(switch.mac for switch in new)
        if new:
            async_add_entities(new)

    async_add_entities(entities)
    entry.async_on_unload(
        async_dispatcher_connect(
            hass, coordinator.signal_device_new, add_block_switches
        )
    )
    add_block_switches()


class SprGuestWifiSwitch(SprRouterEntity, SwitchEntity):
    """Toggle the guest SSID (extra BSS) on all access points."""

    _attr_translation_key = "guest_wifi"

    def __init__(self, coordinator: SprCoordinator) -> None:
        super().__init__(coordinator, "guest_wifi")

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data.router.get("guest_wifi_enabled"))

    async def _set(self, enabled: bool) -> None:
        try:
            await self.coordinator.api.set_guest_wifi(enabled)
        except SprApiError as err:
            raise HomeAssistantError(f"Setting guest wifi failed: {err}") from err
        await self.coordinator.async_request_refresh()

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._set(True)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._set(False)


class SprBlockSwitch(SprDeviceEntity, SwitchEntity):
    """Cut one device's internet access (the 'wan' policy).

    Disabled by default: a busy network would otherwise flood HA with
    switches nobody automates. Enable the ones you need.
    """

    _attr_translation_key = "block_internet"
    _attr_entity_registry_enabled_default = False

    def __init__(self, coordinator: SprCoordinator, mac: str) -> None:
        super().__init__(coordinator, mac, "block")
        self.mac = mac

    @property
    def is_on(self) -> bool:
        device = self.device
        return device is not None and device.blocked

    async def _set(self, blocked: bool) -> None:
        try:
            await self.coordinator.api.set_device_blocked(self.mac, blocked)
        except SprApiError as err:
            raise HomeAssistantError(f"Updating device block failed: {err}") from err
        await self.coordinator.async_request_refresh()

    async def async_turn_on(self, **kwargs: Any) -> None:
        await self._set(True)

    async def async_turn_off(self, **kwargs: Any) -> None:
        await self._set(False)
