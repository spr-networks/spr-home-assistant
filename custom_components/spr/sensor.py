"""Sensors: WAN throughput and totals, client count, uptime, load, WAN IP."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from homeassistant.components.sensor import (
    SensorDeviceClass,
    SensorEntity,
    SensorEntityDescription,
    SensorStateClass,
)
from homeassistant.const import EntityCategory, UnitOfDataRate, UnitOfInformation
from homeassistant.core import HomeAssistant
from homeassistant.helpers.entity_platform import AddConfigEntryEntitiesCallback
from homeassistant.util import dt as dt_util

from .coordinator import SprConfigEntry, SprCoordinator, SprData
from .entity import SprRouterEntity


@dataclass(frozen=True, kw_only=True)
class SprSensorDescription(SensorEntityDescription):
    value_fn: Callable[[SprData], Any]


def _uptime_to_boot_time(data: SprData) -> datetime | None:
    uptime = data.router.get("uptime_seconds") or 0
    if not uptime:
        return None
    # round to the minute so the timestamp doesn't jitter every poll
    boot = dt_util.utcnow().timestamp() - uptime
    return dt_util.utc_from_timestamp(boot - boot % 60)


SENSORS: tuple[SprSensorDescription, ...] = (
    SprSensorDescription(
        key="wan_download_rate",
        translation_key="wan_download_rate",
        device_class=SensorDeviceClass.DATA_RATE,
        native_unit_of_measurement=UnitOfDataRate.BITS_PER_SECOND,
        suggested_unit_of_measurement=UnitOfDataRate.MEGABITS_PER_SECOND,
        state_class=SensorStateClass.MEASUREMENT,
        suggested_display_precision=2,
        value_fn=lambda data: data.traffic.get("wan_rx_rate_bps"),
    ),
    SprSensorDescription(
        key="wan_upload_rate",
        translation_key="wan_upload_rate",
        device_class=SensorDeviceClass.DATA_RATE,
        native_unit_of_measurement=UnitOfDataRate.BITS_PER_SECOND,
        suggested_unit_of_measurement=UnitOfDataRate.MEGABITS_PER_SECOND,
        state_class=SensorStateClass.MEASUREMENT,
        suggested_display_precision=2,
        value_fn=lambda data: data.traffic.get("wan_tx_rate_bps"),
    ),
    SprSensorDescription(
        key="wan_download_total",
        translation_key="wan_download_total",
        device_class=SensorDeviceClass.DATA_SIZE,
        native_unit_of_measurement=UnitOfInformation.BYTES,
        suggested_unit_of_measurement=UnitOfInformation.GIGABYTES,
        state_class=SensorStateClass.TOTAL_INCREASING,
        suggested_display_precision=2,
        value_fn=lambda data: data.traffic.get("wan_rx_bytes"),
    ),
    SprSensorDescription(
        key="wan_upload_total",
        translation_key="wan_upload_total",
        device_class=SensorDeviceClass.DATA_SIZE,
        native_unit_of_measurement=UnitOfInformation.BYTES,
        suggested_unit_of_measurement=UnitOfInformation.GIGABYTES,
        state_class=SensorStateClass.TOTAL_INCREASING,
        suggested_display_precision=2,
        value_fn=lambda data: data.traffic.get("wan_tx_bytes"),
    ),
    SprSensorDescription(
        key="connected_clients",
        translation_key="connected_clients",
        state_class=SensorStateClass.MEASUREMENT,
        value_fn=lambda data: data.router.get("clients_connected"),
    ),
    SprSensorDescription(
        key="wan_ip",
        translation_key="wan_ip",
        entity_category=EntityCategory.DIAGNOSTIC,
        value_fn=lambda data: data.router.get("wan_ip") or None,
    ),
    SprSensorDescription(
        key="boot_time",
        translation_key="boot_time",
        device_class=SensorDeviceClass.TIMESTAMP,
        entity_category=EntityCategory.DIAGNOSTIC,
        value_fn=_uptime_to_boot_time,
    ),
    SprSensorDescription(
        key="load_1m",
        translation_key="load_1m",
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda data: data.router.get("load_1m"),
    ),
    SprSensorDescription(
        key="load_5m",
        translation_key="load_5m",
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda data: data.router.get("load_5m"),
    ),
    SprSensorDescription(
        key="load_15m",
        translation_key="load_15m",
        state_class=SensorStateClass.MEASUREMENT,
        entity_category=EntityCategory.DIAGNOSTIC,
        entity_registry_enabled_default=False,
        value_fn=lambda data: data.router.get("load_15m"),
    ),
)


async def async_setup_entry(
    hass: HomeAssistant,
    entry: SprConfigEntry,
    async_add_entities: AddConfigEntryEntitiesCallback,
) -> None:
    coordinator = entry.runtime_data
    async_add_entities(
        SprRouterSensor(coordinator, description) for description in SENSORS
    )


class SprRouterSensor(SprRouterEntity, SensorEntity):
    entity_description: SprSensorDescription

    def __init__(
        self, coordinator: SprCoordinator, description: SprSensorDescription
    ) -> None:
        super().__init__(coordinator, description.key)
        self.entity_description = description

    @property
    def native_value(self) -> Any:
        return self.entity_description.value_fn(self.coordinator.data)
