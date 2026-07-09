"""Update entity: SPR release version vs newest published release."""

from __future__ import annotations

from homeassistant.components.update import UpdateDeviceClass, UpdateEntity
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback

from .coordinator import SprConfigEntry, SprCoordinator
from .entity import SprRouterEntity


async def async_setup_entry(
    hass: HomeAssistant,
    entry: SprConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    async_add_entities([SprUpdateEntity(entry.runtime_data)])


class SprUpdateEntity(SprRouterEntity, UpdateEntity):
    """Read-only: shows when a newer SPR release is published.

    Installing is left to SPR's own update flow (or its auto-update),
    which verifies and restarts containers safely.
    """

    _attr_device_class = UpdateDeviceClass.FIRMWARE
    _attr_translation_key = "spr_release"
    _attr_release_url = "https://github.com/spr-networks/super/releases"

    def __init__(self, coordinator: SprCoordinator) -> None:
        super().__init__(coordinator, "update")

    @property
    def installed_version(self) -> str | None:
        return self.coordinator.data.router.get("version") or None

    @property
    def latest_version(self) -> str | None:
        # fall back to installed so the entity shows "up to date" when the
        # router tracks a channel we can't compare against
        return (
            self.coordinator.data.router.get("latest_version")
            or self.installed_version
        )
