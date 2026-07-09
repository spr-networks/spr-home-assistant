"""Integration tests: config flow and coordinator against a mocked ha_sync API."""

from __future__ import annotations

from aiohttp import web
import pytest

from homeassistant.config_entries import SOURCE_USER
from homeassistant.const import CONF_HOST, CONF_PORT, CONF_TOKEN, STATE_HOME
from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er
from homeassistant.setup import async_setup_component

from pytest_homeassistant_custom_component.common import MockConfigEntry
from pytest_homeassistant_custom_component.test_util.aiohttp import AiohttpClientMocker

DOMAIN = "spr"
TOKEN = "test-pairing-token"

PROBE = {
    "product": "spr",
    "api": 1,
    "id": "routerid123",
    "hostname": "spr-test",
    "version": "1.0.0",
}

STATE = {
    "router": {
        "hostname": "spr-test",
        "version": "1.0.0",
        "latest_version": "1.0.5",
        "update_available": True,
        "uptime_seconds": 7200,
        "load_1m": 0.2,
        "load_5m": 0.1,
        "load_15m": 0.05,
        "wan_ip": "198.51.100.9",
        "wan_iface": "eth0",
        "wan_up": True,
        "guest_wifi_enabled": True,
        "clients_connected": 2,
    },
    "traffic": {
        "wan_rx_bytes": 9000,
        "wan_tx_bytes": 1000,
        "wan_rx_rate_bps": 1_000_000,
        "wan_tx_rate_bps": 250_000,
    },
    "devices": [
        {
            "mac": "aa:bb:cc:dd:ee:01",
            "name": "phone",
            "ip": "192.168.2.100",
            "groups": [],
            "tags": [],
            "connected": True,
            "wired": False,
            "iface": "wlan0",
            "signal": -48,
            "last_seen": 1783550000,
            "rx_bytes": 9000,
            "tx_bytes": 1000,
            "blocked": False,
            "guest": False,
        },
    ],
    "timestamp": 1783550000,
}


@pytest.fixture
def mock_spr_api(aioclient_mock: AiohttpClientMocker) -> AiohttpClientMocker:
    base = "http://192.0.2.1:8321"
    aioclient_mock.get(f"{base}/api/probe", json=PROBE)
    aioclient_mock.get(f"{base}/api/state", json=STATE)
    # keep the SSE listener from erroring in the background
    aioclient_mock.get(f"{base}/api/events", exc=OSError("no sse in tests"))
    return aioclient_mock


async def test_user_flow_creates_entry(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_USER}
    )
    assert result["type"] == "form"

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"],
        {CONF_HOST: "192.0.2.1", CONF_PORT: 8321, CONF_TOKEN: TOKEN},
    )
    assert result["type"] == "create_entry", result
    assert result["title"] == "spr-test"
    assert result["result"].unique_id == "routerid123"


async def test_setup_creates_entities(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    entry = MockConfigEntry(
        domain=DOMAIN,
        unique_id="routerid123",
        data={CONF_HOST: "192.0.2.1", CONF_PORT: 8321, CONF_TOKEN: TOKEN},
    )
    entry.add_to_hass(hass)
    assert await async_setup_component(hass, DOMAIN, {})
    await hass.async_block_till_done()
    assert entry.state.value == "loaded"

    registry = er.async_get(hass)
    tracker_id = registry.async_get_entity_id(
        "device_tracker", DOMAIN, "aa:bb:cc:dd:ee:01"
    )
    assert tracker_id is not None
    tracker = hass.states.get(tracker_id)
    assert tracker is not None
    assert tracker.state == STATE_HOME
    # ip/mac/host_name attributes are what HA's DHCP discovery consumes
    assert tracker.attributes["ip"] == "192.168.2.100"
    assert tracker.attributes["mac"] == "aa:bb:cc:dd:ee:01"
    assert tracker.attributes["host_name"] == "phone"
    assert tracker.attributes["source_type"] == "router"

    clients = hass.states.get("sensor.spr_test_connected_clients")
    assert clients is not None and clients.state == "2"

    wan = hass.states.get("binary_sensor.spr_test_wan_status")
    assert wan is not None and wan.state == "on"

    guest = hass.states.get("switch.spr_test_guest_wi_fi")
    assert guest is not None and guest.state == "on"

    update = hass.states.get("update.spr_test_spr_release")
    assert update is not None and update.state == "on"  # 1.0.0 -> 1.0.5
