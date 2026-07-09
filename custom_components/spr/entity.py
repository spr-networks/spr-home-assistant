"""Base entities for the SPR integration."""

from __future__ import annotations

from homeassistant.helpers.device_registry import (
    CONNECTION_NETWORK_MAC,
    DeviceInfo,
    format_mac,
)
from homeassistant.helpers.update_coordinator import CoordinatorEntity

from .const import ATTR_MANUFACTURER, ATTR_MODEL, DOMAIN
from .coordinator import SprCoordinator, SprDevice


class SprRouterEntity(CoordinatorEntity[SprCoordinator]):
    """Entity attached to the router hub device."""

    _attr_has_entity_name = True

    def __init__(self, coordinator: SprCoordinator, key: str) -> None:
        super().__init__(coordinator)
        entry = coordinator.config_entry
        unique = entry.unique_id or entry.entry_id
        self._attr_unique_id = f"{unique}_{key}"
        self._attr_device_info = DeviceInfo(
            identifiers={(DOMAIN, unique)},
            manufacturer=ATTR_MANUFACTURER,
            model=ATTR_MODEL,
            name=coordinator.data.router.get("hostname") or "SPR",
            sw_version=coordinator.data.router.get("version"),
            configuration_url=f"http://{entry.data['host']}",
        )


class SprDeviceEntity(CoordinatorEntity[SprCoordinator]):
    """Entity attached to a client device on the SPR network."""

    _attr_has_entity_name = True

    def __init__(self, coordinator: SprCoordinator, mac: str, key: str) -> None:
        super().__init__(coordinator)
        entry = coordinator.config_entry
        self._mac = format_mac(mac)
        self._attr_unique_id = f"{self._mac}_{key}"
        device = self.device
        self._attr_device_info = DeviceInfo(
            connections={(CONNECTION_NETWORK_MAC, self._mac)},
            default_name=(device.name if device else None) or self._mac,
            via_device=(DOMAIN, entry.unique_id or entry.entry_id),
        )

    @property
    def device(self) -> SprDevice | None:
        return self.coordinator.data.devices.get(self._mac)

    @property
    def available(self) -> bool:
        return super().available and self.device is not None
