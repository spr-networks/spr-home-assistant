"""Async client for the SPR ha_sync plugin API."""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Any

import aiohttp

_LOGGER = logging.getLogger(__name__)

API_TIMEOUT = aiohttp.ClientTimeout(total=10)


class SprApiError(Exception):
    """Base error talking to the SPR plugin."""


class SprAuthError(SprApiError):
    """Invalid or rotated pairing token."""


class SprApiClient:
    """Talks to the ha_sync plugin running on the SPR router."""

    def __init__(
        self,
        session: aiohttp.ClientSession,
        host: str,
        port: int,
        token: str | None = None,
    ) -> None:
        self._session = session
        self._host = host
        self._port = port
        self._token = token

    @property
    def base_url(self) -> str:
        host = self._host
        if ":" in host and not host.startswith("["):
            host = f"[{host}]"  # bare IPv6 from discovery
        return f"http://{host}:{self._port}"

    def _headers(self) -> dict[str, str]:
        if not self._token:
            return {}
        return {"Authorization": f"Bearer {self._token}"}

    async def _request(
        self, method: str, path: str, payload: dict[str, Any] | None = None
    ) -> Any:
        try:
            resp = await self._session.request(
                method,
                f"{self.base_url}{path}",
                json=payload,
                headers=self._headers(),
                timeout=API_TIMEOUT,
            )
        except (aiohttp.ClientError, asyncio.TimeoutError) as err:
            raise SprApiError(f"Error connecting to SPR at {self._host}: {err}") from err

        if resp.status == 401:
            raise SprAuthError("SPR rejected the pairing token")
        if resp.status >= 400:
            body = await resp.text()
            raise SprApiError(f"SPR API error {resp.status}: {body[:200]}")
        return await resp.json()

    async def probe(self) -> dict[str, Any]:
        """Unauthenticated identify call used by discovery/config flow."""
        return await self._request("GET", "/api/probe")

    async def get_state(self) -> dict[str, Any]:
        """Full state snapshot: router, traffic, devices."""
        return await self._request("GET", "/api/state")

    async def set_device_blocked(self, mac: str, blocked: bool) -> None:
        await self._request("PUT", f"/api/device/{mac}/block", {"blocked": blocked})

    async def set_guest_wifi(self, enabled: bool) -> None:
        await self._request("PUT", "/api/guest_wifi", {"enabled": enabled})

    async def restart_router(self) -> None:
        await self._request("POST", "/api/system/restart")

    async def wake_on_lan(self, mac: str) -> None:
        await self._request("POST", "/api/wol", {"mac": mac})

    async def listen_events(self, callback) -> None:
        """Long-running SSE listener; invokes callback(event_dict) per event.

        Raises SprApiError/SprAuthError when the stream drops so the caller
        can back off and reconnect.
        """
        try:
            resp = await self._session.get(
                f"{self.base_url}/api/events",
                headers=self._headers(),
                # sock_read > the server's 30s keepalive so a half-open TCP
                # drop (no FIN) is detected and the loop reconnects instead
                # of wedging forever
                timeout=aiohttp.ClientTimeout(
                    total=None, sock_connect=10, sock_read=60
                ),
            )
        except (aiohttp.ClientError, asyncio.TimeoutError) as err:
            raise SprApiError(f"SSE connect failed: {err}") from err

        if resp.status == 401:
            raise SprAuthError("SPR rejected the pairing token")
        if resp.status >= 400:
            raise SprApiError(f"SSE error {resp.status}")

        try:
            async for raw_line in resp.content:
                line = raw_line.decode("utf-8", "replace").strip()
                if not line.startswith("data:"):
                    continue
                try:
                    event = json.loads(line[len("data:") :].strip())
                except ValueError:
                    continue
                callback(event)
        except (aiohttp.ClientError, asyncio.TimeoutError) as err:
            raise SprApiError(f"SSE stream dropped: {err}") from err
        finally:
            resp.close()
