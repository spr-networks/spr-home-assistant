"""Config flow for the SPR integration.

The router advertises itself over mDNS; the discovery step reads an
unauthenticated static identify document to present it, then the user
supplies a read-only scoped SPR token. Manual setup (URL + token) also
works. A discovery broadcast is treated as a hint only — it never rewrites
an existing entry's URL, so a forged advertisement cannot redirect the
token to an attacker.
"""

from __future__ import annotations

import logging
from typing import Any

import voluptuous as vol

from homeassistant.config_entries import (
    ConfigEntry,
    ConfigFlow,
    ConfigFlowResult,
    OptionsFlowWithReload,
)
from homeassistant.const import CONF_TOKEN, CONF_URL, CONF_VERIFY_SSL
from homeassistant.core import callback
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo

from .api import SprApiClient, SprApiError, SprAuthError, fetch_discovery
from .const import (
    CONF_CONSIDER_HOME,
    CONF_TRACK_NEW_DEVICES,
    DEFAULT_CONSIDER_HOME,
    DEFAULT_TRACK_NEW_DEVICES,
    DEFAULT_VERIFY_SSL,
    DOMAIN,
)

_LOGGER = logging.getLogger(__name__)

USER_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_URL, default="https://192.168.2.1"): str,
        vol.Required(CONF_TOKEN): str,
        vol.Optional(CONF_VERIFY_SSL, default=DEFAULT_VERIFY_SSL): bool,
    }
)

OPTIONS_SCHEMA = vol.Schema(
    {
        vol.Optional(CONF_CONSIDER_HOME, default=DEFAULT_CONSIDER_HOME): vol.All(
            vol.Coerce(int), vol.Range(min=0, max=3600)
        ),
        vol.Optional(
            CONF_TRACK_NEW_DEVICES, default=DEFAULT_TRACK_NEW_DEVICES
        ): bool,
    }
)


def _normalize_url(url: str) -> str:
    url = url.strip().rstrip("/")
    if not url.startswith(("http://", "https://")):
        url = f"https://{url}"
    return url


class SprConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle SPR config flow: router URL + read-only scoped token."""

    VERSION = 1

    def __init__(self) -> None:
        self._discovered_url: str | None = None
        self._discovered_name: str = "SPR"

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> SprOptionsFlow:
        return SprOptionsFlow()

    async def _async_validate(
        self, url: str, token: str, verify_ssl: bool
    ) -> tuple[dict[str, str], dict[str, Any] | None]:
        """Try the credentials; return (errors, probe_info)."""
        session = async_get_clientsession(self.hass, verify_ssl=verify_ssl)
        api = SprApiClient(session, url, token)
        try:
            probe = await api.probe()
            await api.get_state()
        except SprAuthError:
            return {"base": "invalid_auth"}, None
        except SprApiError:
            return {"base": "cannot_connect"}, None
        return {}, probe

    async def async_step_user(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        if user_input is not None:
            url = _normalize_url(user_input[CONF_URL])
            errors, probe = await self._async_validate(
                url, user_input[CONF_TOKEN], user_input[CONF_VERIFY_SSL]
            )
            if not errors:
                router_id = probe.get("id") or ""
                if router_id:
                    await self.async_set_unique_id(router_id)
                    self._abort_if_unique_id_configured(updates={CONF_URL: url})
                return self.async_create_entry(
                    title=probe.get("hostname") or "SPR",
                    data={
                        CONF_URL: url,
                        CONF_TOKEN: user_input[CONF_TOKEN],
                        CONF_VERIFY_SSL: user_input[CONF_VERIFY_SSL],
                    },
                )
        return self.async_show_form(
            step_id="user",
            data_schema=self.add_suggested_values_to_schema(USER_SCHEMA, user_input),
            errors=errors,
        )

    async def async_step_zeroconf(
        self, discovery_info: ZeroconfServiceInfo
    ) -> ConfigFlowResult:
        """A router advertised _spr-ha._tcp.

        The advertisement is only a trigger. We read the router's identity
        from its unauthenticated static document to present it, dedup on the
        router id, and then ask the user for a token. We never write the
        discovered URL onto an existing entry, so a forged advertisement
        can't redirect a configured router's token.
        """
        host = discovery_info.host
        if ":" in host and not host.startswith("["):
            host = f"[{host}]"  # bare IPv6
        url = f"https://{host}"

        # discovery fetch is unauthenticated and self-signed on the LAN
        session = async_get_clientsession(self.hass, verify_ssl=False)
        try:
            info = await fetch_discovery(session, url)
        except SprApiError:
            return self.async_abort(reason="cannot_connect")

        router_id = info.get("id")
        if not router_id:
            return self.async_abort(reason="cannot_connect")

        await self.async_set_unique_id(router_id)
        # dedup only — deliberately no updates={CONF_URL: ...}
        self._abort_if_unique_id_configured()

        self._discovered_url = url
        self._discovered_name = info.get("name") or "SPR"
        self.context["title_placeholders"] = {"name": self._discovered_name}
        return await self.async_step_discovery_confirm()

    async def async_step_discovery_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Confirm a discovered router and collect the token."""
        errors: dict[str, str] = {}
        assert self._discovered_url is not None
        if user_input is not None:
            url = _normalize_url(user_input[CONF_URL])
            errors, probe = await self._async_validate(
                url, user_input[CONF_TOKEN], user_input[CONF_VERIFY_SSL]
            )
            if not errors:
                return self.async_create_entry(
                    title=probe.get("hostname") or self._discovered_name,
                    data={
                        CONF_URL: url,
                        CONF_TOKEN: user_input[CONF_TOKEN],
                        CONF_VERIFY_SSL: user_input[CONF_VERIFY_SSL],
                    },
                )
        schema = vol.Schema(
            {
                vol.Required(CONF_URL, default=self._discovered_url): str,
                vol.Required(CONF_TOKEN): str,
                vol.Optional(CONF_VERIFY_SSL, default=DEFAULT_VERIFY_SSL): bool,
            }
        )
        return self.async_show_form(
            step_id="discovery_confirm",
            data_schema=self.add_suggested_values_to_schema(schema, user_input),
            description_placeholders={"name": self._discovered_name},
            errors=errors,
        )

    async def async_step_reauth(self, entry_data: dict[str, Any]) -> ConfigFlowResult:
        """The SPR token was revoked or rotated on the router."""
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        entry = self._get_reauth_entry()
        if user_input is not None:
            errors, _ = await self._async_validate(
                entry.data[CONF_URL],
                user_input[CONF_TOKEN],
                entry.data.get(CONF_VERIFY_SSL, DEFAULT_VERIFY_SSL),
            )
            if not errors:
                return self.async_update_reload_and_abort(
                    entry, data_updates={CONF_TOKEN: user_input[CONF_TOKEN]}
                )
        return self.async_show_form(
            step_id="reauth_confirm",
            data_schema=vol.Schema({vol.Required(CONF_TOKEN): str}),
            errors=errors,
        )


class SprOptionsFlow(OptionsFlowWithReload):
    """Options: consider_home grace period, tracking defaults."""

    async def async_step_init(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        if user_input is not None:
            return self.async_create_entry(data=user_input)
        return self.async_show_form(
            step_id="init",
            data_schema=self.add_suggested_values_to_schema(
                OPTIONS_SCHEMA, self.config_entry.options
            ),
        )
