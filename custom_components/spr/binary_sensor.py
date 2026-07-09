"""Binary sensors: WAN connectivity."""

from __future__ import annotations

from homeassistant.components.binary_sensor import (
    BinarySensorDeviceClass,
    BinarySensorEntity,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .coordinator import SprConfigEntry, SprCoordinator
from .entity import SprRouterEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: SprConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    async_add_entities([SprWanStatusSensor(entry.runtime_data)])


class SprWanStatusSensor(SprRouterEntity, BinarySensorEntity):
    """On when the WAN uplink is up."""

    _attr_device_class = BinarySensorDeviceClass.CONNECTIVITY
    _attr_translation_key = "wan_status"

    def __init__(self, coordinator: SprCoordinator) -> None:
        super().__init__(coordinator, "wan_status")

    @property
    def is_on(self) -> bool:
        return bool(self.coordinator.data.router.get("wan_up"))

    @property
    def extra_state_attributes(self) -> dict[str, object]:
        return {"interface": self.coordinator.data.router.get("wan_iface") or ""}
