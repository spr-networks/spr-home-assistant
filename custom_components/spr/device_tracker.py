"""Device tracker platform: presence for every client on the SPR network.

These ScannerEntities publish ip/mac/hostname attributes, which Home
Assistant's DHCP discovery watches — so SPR acts as a network discovery
provider that teaches HA about devices on the LAN.
"""

from __future__ import annotations

from homeassistant.components.device_tracker import ScannerEntity
from homeassistant.core import HomeAssistant, callback
from homeassistant.helpers.dispatcher import async_dispatcher_connect
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .const import CONF_TRACK_NEW_DEVICES, DEFAULT_TRACK_NEW_DEVICES
from .coordinator import SprConfigEntry, SprCoordinator
from .entity import SprDeviceEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: SprConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    """Track known devices and pick up new ones as they join."""
    coordinator = entry.runtime_data
    tracked: set[str] = set()

    @callback
    def add_new_devices(macs: list[str] | None = None) -> None:
        new = [
            SprScannerEntity(coordinator, mac)
            for mac in coordinator.data.devices
            if mac not in tracked
        ]
        tracked.update(entity.mac_address for entity in new)
        if new:
            async_add_entities(new)

    entry.async_on_unload(
        async_dispatcher_connect(hass, coordinator.signal_device_new, add_new_devices)
    )
    add_new_devices()


class SprScannerEntity(SprDeviceEntity, ScannerEntity):
    """Presence of one client device, fed by SPR's live station data."""

    # the tracker is the client device's main entity: take the device name
    _attr_name = None

    def __init__(self, coordinator: SprCoordinator, mac: str) -> None:
        super().__init__(coordinator, mac, "tracker")
        self._attr_unique_id = self._mac
        self._track_new = coordinator.config_entry.options.get(
            CONF_TRACK_NEW_DEVICES, DEFAULT_TRACK_NEW_DEVICES
        )

    @property
    def entity_registry_enabled_default(self) -> bool:
        """Honor the track-new-devices option.

        ScannerEntity's base property auto-disables trackers whose MAC has no
        device registry entry; since this integration is read-only it creates
        no client device entries, so that heuristic would disable everything.
        """
        return self._track_new

    @property
    def is_connected(self) -> bool:
        device = self.device
        return device is not None and device.connected

    @property
    def ip_address(self) -> str | None:
        device = self.device
        return device.ip or None if device else None

    @property
    def mac_address(self) -> str:
        return self._mac

    @property
    def hostname(self) -> str | None:
        device = self.device
        return device.name or None if device else None

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        device = self.device
        if device is None:
            return {}
        attrs: dict[str, object] = {
            "wired": device.wired,
            "interface": device.iface,
            "groups": device.groups,
            "tags": device.tags,
            "guest": device.guest,
        }
        if device.signal:
            attrs["signal_dbm"] = device.signal
        if device.last_seen:
            attrs["last_seen"] = device.last_seen.isoformat()
        return attrs
