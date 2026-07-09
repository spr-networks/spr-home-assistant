"""Config flow for the SPR integration."""

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
from homeassistant.const import CONF_HOST, CONF_PORT, CONF_TOKEN
from homeassistant.core import callback
from homeassistant.helpers.aiohttp_client import async_get_clientsession
from homeassistant.helpers.service_info.zeroconf import ZeroconfServiceInfo

from .api import SprApiClient, SprApiError, SprAuthError
from .const import (
    CONF_CONSIDER_HOME,
    CONF_TRACK_NEW_DEVICES,
    DEFAULT_CONSIDER_HOME,
    DEFAULT_PORT,
    DEFAULT_TRACK_NEW_DEVICES,
    DOMAIN,
)

_LOGGER = logging.getLogger(__name__)

USER_SCHEMA = vol.Schema(
    {
        vol.Required(CONF_HOST): str,
        vol.Required(CONF_PORT, default=DEFAULT_PORT): int,
        vol.Required(CONF_TOKEN): str,
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


class SprConfigFlow(ConfigFlow, domain=DOMAIN):
    """Handle SPR config flow: manual entry plus zeroconf discovery."""

    VERSION = 1

    def __init__(self) -> None:
        self._host: str | None = None
        self._port: int = DEFAULT_PORT
        self._name: str = "SPR"

    @staticmethod
    @callback
    def async_get_options_flow(config_entry: ConfigEntry) -> SprOptionsFlow:
        return SprOptionsFlow()

    async def _async_validate(
        self, host: str, port: int, token: str
    ) -> tuple[dict[str, str], dict[str, Any] | None]:
        """Try the credentials; return (errors, probe_info)."""
        api = SprApiClient(async_get_clientsession(self.hass), host, port, token)
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
        """Manual setup: host, port, pairing token."""
        errors: dict[str, str] = {}
        if user_input is not None:
            errors, probe = await self._async_validate(
                user_input[CONF_HOST], user_input[CONF_PORT], user_input[CONF_TOKEN]
            )
            if not errors:
                router_id = probe.get("id") or ""
                if router_id:
                    await self.async_set_unique_id(router_id)
                    self._abort_if_unique_id_configured(
                        updates={
                            CONF_HOST: user_input[CONF_HOST],
                            CONF_PORT: user_input[CONF_PORT],
                        }
                    )
                return self.async_create_entry(
                    title=probe.get("hostname") or "SPR", data=user_input
                )
        return self.async_show_form(
            step_id="user",
            data_schema=self.add_suggested_values_to_schema(USER_SCHEMA, user_input),
            errors=errors,
        )

    async def async_step_zeroconf(
        self, discovery_info: ZeroconfServiceInfo
    ) -> ConfigFlowResult:
        """Discovered a router advertising _spr-ha._tcp."""
        router_id = discovery_info.properties.get("id")
        if not router_id:
            return self.async_abort(reason="cannot_connect")

        self._host = discovery_info.host
        self._port = discovery_info.port or DEFAULT_PORT
        self._name = discovery_info.name.split(".")[0]

        await self.async_set_unique_id(router_id)
        self._abort_if_unique_id_configured(
            updates={CONF_HOST: self._host, CONF_PORT: self._port}
        )
        self.context["title_placeholders"] = {"name": self._name}
        return await self.async_step_pair()

    async def async_step_pair(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        """Ask for the pairing token after discovery."""
        errors: dict[str, str] = {}
        if user_input is not None:
            assert self._host is not None
            errors, probe = await self._async_validate(
                self._host, self._port, user_input[CONF_TOKEN]
            )
            if not errors:
                return self.async_create_entry(
                    title=probe.get("hostname") or self._name,
                    data={
                        CONF_HOST: self._host,
                        CONF_PORT: self._port,
                        CONF_TOKEN: user_input[CONF_TOKEN],
                    },
                )
        return self.async_show_form(
            step_id="pair",
            data_schema=vol.Schema({vol.Required(CONF_TOKEN): str}),
            description_placeholders={"name": self._name, "host": self._host or ""},
            errors=errors,
        )

    async def async_step_reauth(self, entry_data: dict[str, Any]) -> ConfigFlowResult:
        """Pairing token was rotated on the router."""
        return await self.async_step_reauth_confirm()

    async def async_step_reauth_confirm(
        self, user_input: dict[str, Any] | None = None
    ) -> ConfigFlowResult:
        errors: dict[str, str] = {}
        entry = self._get_reauth_entry()
        if user_input is not None:
            errors, _ = await self._async_validate(
                entry.data[CONF_HOST], entry.data[CONF_PORT], user_input[CONF_TOKEN]
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
