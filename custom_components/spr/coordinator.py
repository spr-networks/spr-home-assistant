"""DataUpdateCoordinator for SPR: one poll cycle shared by all entities."""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import Any

from homeassistant.config_entries import ConfigEntry
from homeassistant.core import HomeAssistant, callback
from homeassistant.exceptions import ConfigEntryAuthFailed
from homeassistant.helpers.device_registry import format_mac
from homeassistant.helpers.dispatcher import async_dispatcher_send
from homeassistant.helpers.update_coordinator import DataUpdateCoordinator, UpdateFailed
from homeassistant.util import dt as dt_util

from .api import SprApiClient, SprApiError, SprAuthError
from .const import (
    CONF_CONSIDER_HOME,
    DEFAULT_CONSIDER_HOME,
    DEFAULT_SCAN_INTERVAL,
    DOMAIN,
)

_LOGGER = logging.getLogger(__name__)

type SprConfigEntry = ConfigEntry[SprCoordinator]


@dataclass
class SprDevice:
    """A client device on the SPR network, with consider_home applied."""

    mac: str
    name: str = ""
    ip: str = ""
    groups: list[str] = field(default_factory=list)
    tags: list[str] = field(default_factory=list)
    connected: bool = False
    wired: bool = False
    iface: str = ""
    signal: int = 0
    last_seen: datetime | None = None
    rx_bytes: int = 0
    tx_bytes: int = 0
    blocked: bool = False
    guest: bool = False
    # raw connect state before the consider_home grace period is applied
    raw_connected: bool = False


@dataclass
class SprData:
    """Parsed /api/state snapshot."""

    router: dict[str, Any] = field(default_factory=dict)
    traffic: dict[str, Any] = field(default_factory=dict)
    devices: dict[str, SprDevice] = field(default_factory=dict)


class SprCoordinator(DataUpdateCoordinator[SprData]):
    """Polls the ha_sync plugin and layers presence bookkeeping on top."""

    config_entry: SprConfigEntry

    def __init__(
        self,
        hass: HomeAssistant,
        config_entry: SprConfigEntry,
        api: SprApiClient,
    ) -> None:
        super().__init__(
            hass,
            _LOGGER,
            name=DOMAIN,
            config_entry=config_entry,
            update_interval=DEFAULT_SCAN_INTERVAL,
        )
        self.api = api
        self._devices: dict[str, SprDevice] = {}
        self._event_task: asyncio.Task | None = None

    @property
    def signal_device_new(self) -> str:
        """Dispatcher signal fired when unseen MACs appear in a poll."""
        return f"{DOMAIN}-{self.config_entry.entry_id}-device-new"

    @property
    def consider_home(self) -> timedelta:
        return timedelta(
            seconds=self.config_entry.options.get(
                CONF_CONSIDER_HOME, DEFAULT_CONSIDER_HOME
            )
        )

    async def _async_update_data(self) -> SprData:
        try:
            async with asyncio.timeout(15):
                raw = await self.api.get_state()
        except SprAuthError as err:
            raise ConfigEntryAuthFailed from err
        except SprApiError as err:
            raise UpdateFailed(str(err)) from err

        now = dt_util.utcnow()
        new_macs: list[str] = []
        seen_macs: set[str] = set()

        for raw_dev in raw.get("devices") or []:
            mac = format_mac(raw_dev.get("mac", ""))
            if not mac or mac == "00:00:00:00:00:00":
                continue
            seen_macs.add(mac)
            device = self._devices.get(mac)
            if device is None:
                device = SprDevice(mac=mac)
                self._devices[mac] = device
                new_macs.append(mac)

            device.name = raw_dev.get("name") or device.name
            device.ip = raw_dev.get("ip") or device.ip
            device.groups = raw_dev.get("groups") or []
            device.tags = raw_dev.get("tags") or []
            device.wired = bool(raw_dev.get("wired"))
            device.iface = raw_dev.get("iface") or ""
            device.signal = raw_dev.get("signal") or 0
            device.rx_bytes = raw_dev.get("rx_bytes") or 0
            device.tx_bytes = raw_dev.get("tx_bytes") or 0
            device.blocked = bool(raw_dev.get("blocked"))
            device.guest = bool(raw_dev.get("guest"))
            device.raw_connected = bool(raw_dev.get("connected"))

            if device.raw_connected:
                device.last_seen = now
            elif last_seen := raw_dev.get("last_seen"):
                reported = dt_util.utc_from_timestamp(last_seen)
                if device.last_seen is None or reported > device.last_seen:
                    device.last_seen = reported

            # consider_home: keep a sleeping phone "home" for a grace period
            device.connected = device.raw_connected or (
                device.last_seen is not None
                and now - device.last_seen < self.consider_home
            )

        # devices deleted on the router: drop them so their entities go
        # unavailable instead of freezing on stale state
        for mac in list(self._devices):
            if mac not in seen_macs:
                del self._devices[mac]

        data = SprData(
            router=raw.get("router") or {},
            traffic=raw.get("traffic") or {},
            devices=self._devices,
        )

        if new_macs and self.data is not None:
            async_dispatcher_send(self.hass, self.signal_device_new, new_macs)

        return data

    # ---- SSE push channel: turns plugin events into immediate refreshes ----

    def start_event_listener(self) -> None:
        if self._event_task is None:
            self._event_task = self.config_entry.async_create_background_task(
                self.hass, self._event_loop(), name=f"{DOMAIN}-sse"
            )

    async def stop_event_listener(self) -> None:
        if self._event_task is not None:
            self._event_task.cancel()
            self._event_task = None

    async def _event_loop(self) -> None:
        backoff = 5
        while True:
            try:
                await self.api.listen_events(self._handle_event)
                backoff = 5
            except SprAuthError:
                _LOGGER.warning("SPR event stream: auth failed, stopping push")
                return
            except SprApiError as err:
                _LOGGER.debug("SPR event stream reconnect in %ss: %s", backoff, err)
            await asyncio.sleep(backoff)
            backoff = min(backoff * 2, 300)

    @callback
    def _handle_event(self, event: dict[str, Any]) -> None:
        """A device connected/disconnected on the router: refresh right away."""
        self.hass.async_create_task(self.async_request_refresh())
