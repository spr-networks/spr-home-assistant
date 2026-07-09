"""Integration tests: config flow and coordinator against a mocked SPR proxy."""

from __future__ import annotations

import pytest

from ipaddress import ip_address

from homeassistant.config_entries import SOURCE_USER, SOURCE_ZEROCONF
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo
from homeassistant.const import (
    CONF_TOKEN,
    CONF_URL,
    CONF_VERIFY_SSL,
    STATE_HOME,
)
from homeassistant.core import HomeAssistant
from homeassistant.helpers import entity_registry as er
from homeassistant.setup import async_setup_component

from pytest_homeassistant_custom_component.common import MockConfigEntry
from pytest_homeassistant_custom_component.test_util.aiohttp import AiohttpClientMocker

DOMAIN = "spr"
TOKEN = "spr-scoped-readonly-token"
BASE = "https://192.0.2.1/plugins/home_assistant/ha/v1"

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


DISCOVERY_URL = (
    "https://192.0.2.1/admin/custom_plugin/home_assistant/static/discovery.json"
)
DISCOVERY_DOC = {"product": "spr", "api": 1, "id": "routerid123", "name": "spr-test"}


@pytest.fixture
def mock_spr_api(aioclient_mock: AiohttpClientMocker) -> AiohttpClientMocker:
    aioclient_mock.get(f"{BASE}/probe", json=PROBE)
    aioclient_mock.get(f"{BASE}/state", json=STATE)
    aioclient_mock.get(DISCOVERY_URL, json=DISCOVERY_DOC)
    # keep the SSE listener from erroring in the background
    aioclient_mock.get(f"{BASE}/events", exc=OSError("no sse in tests"))
    return aioclient_mock


def _zeroconf_info() -> ZeroconfServiceInfo:
    return ZeroconfServiceInfo(
        ip_address=ip_address("192.0.2.1"),
        ip_addresses=[ip_address("192.0.2.1")],
        port=443,
        hostname="spr.local.",
        type="_spr-ha._tcp.local.",
        name="SPR (spr-test)._spr-ha._tcp.local.",
        properties={"product": "spr"},
    )


async def test_zeroconf_discovery_then_token(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_ZEROCONF}, data=_zeroconf_info()
    )
    # discovery reads the unauthenticated static doc, then asks for a token
    assert result["type"] == "form"
    assert result["step_id"] == "discovery_confirm"

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"],
        {CONF_URL: "https://192.0.2.1", CONF_TOKEN: TOKEN, CONF_VERIFY_SSL: False},
    )
    assert result["type"] == "create_entry", result
    assert result["result"].unique_id == "routerid123"


async def test_zeroconf_existing_entry_not_rewritten(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    """A discovery broadcast must not repoint a configured router's URL."""
    entry = MockConfigEntry(
        domain=DOMAIN,
        unique_id="routerid123",
        data={
            CONF_URL: "https://192.168.9.9",
            CONF_TOKEN: TOKEN,
            CONF_VERIFY_SSL: False,
        },
    )
    entry.add_to_hass(hass)

    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_ZEROCONF}, data=_zeroconf_info()
    )
    assert result["type"] == "abort"
    assert result["reason"] == "already_configured"
    # URL is untouched — the forged/benign broadcast did not redirect it
    assert entry.data[CONF_URL] == "https://192.168.9.9"


async def test_user_flow_creates_entry(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    result = await hass.config_entries.flow.async_init(
        DOMAIN, context={"source": SOURCE_USER}
    )
    assert result["type"] == "form"

    result = await hass.config_entries.flow.async_configure(
        result["flow_id"],
        {
            CONF_URL: "https://192.0.2.1",
            CONF_TOKEN: TOKEN,
            CONF_VERIFY_SSL: False,
        },
    )
    assert result["type"] == "create_entry", result
    assert result["title"] == "spr-test"
    assert result["result"].unique_id == "routerid123"
    assert result["data"][CONF_URL] == "https://192.0.2.1"


async def test_setup_creates_entities(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    entry = MockConfigEntry(
        domain=DOMAIN,
        unique_id="routerid123",
        data={
            CONF_URL: "https://192.0.2.1",
            CONF_TOKEN: TOKEN,
            CONF_VERIFY_SSL: False,
        },
    )
    entry.add_to_hass(hass)
    assert await async_setup_component(hass, DOMAIN, {})
    await hass.async_block_till_done()
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

    update = hass.states.get("update.spr_test_spr_release")
    assert update is not None and update.state == "on"  # 1.0.0 -> 1.0.5

    # read-only build: no switches; WoL buttons exist but default-disabled
    assert not [s for s in hass.states.async_entity_ids("switch")]
    assert not [s for s in hass.states.async_entity_ids("button")]
    wake_button = registry.async_get_entity_id(
        "button", DOMAIN, "aa:bb:cc:dd:ee:01_wake_on_lan"
    )
    assert wake_button is not None
    assert registry.async_get(wake_button).disabled_by is not None

    # the only service is wake_on_lan (a GET on the router; no SPR writes)
    assert set(hass.services.async_services().get(DOMAIN, {})) == {"wake_on_lan"}


async def test_only_get_requests(
    hass: HomeAssistant, mock_spr_api: AiohttpClientMocker
) -> None:
    """Every HTTP call the integration makes must be a GET."""
    entry = MockConfigEntry(
        domain=DOMAIN,
        unique_id="routerid123",
        data={
            CONF_URL: "https://192.0.2.1",
            CONF_TOKEN: TOKEN,
            CONF_VERIFY_SSL: False,
        },
    )
    entry.add_to_hass(hass)
    assert await async_setup_component(hass, DOMAIN, {})
    await hass.async_block_till_done()

    methods = {call[0] for call in mock_spr_api.mock_calls}
    assert methods == {"GET"}, methods
