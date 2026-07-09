"""Async client for the SPR ha_sync plugin, via SPR's authenticated proxy.

All requests are GETs to /plugins/home_assistant/ha/v1/* on the router's
API (port 80/443), authenticated with an SPR token scoped read-only to that
path. The integration performs no writes, ever.
"""

from __future__ import annotations

import asyncio
import json
import logging
from typing import Any

import aiohttp

from .const import PROXY_BASE

_LOGGER = logging.getLogger(__name__)

API_TIMEOUT = aiohttp.ClientTimeout(total=10)


class SprApiError(Exception):
    """Base error talking to the SPR plugin."""


class SprAuthError(SprApiError):
    """Invalid or revoked SPR token."""


class SprApiClient:
    """Read-only client for the ha_sync plugin behind SPR's API proxy."""

    def __init__(
        self,
        session: aiohttp.ClientSession,
        url: str,
        token: str | None = None,
    ) -> None:
        self._session = session
        self._base = url.rstrip("/") + PROXY_BASE
        self._token = token

    @property
    def base_url(self) -> str:
        return self._base

    def _headers(self) -> dict[str, str]:
        if not self._token:
            return {}
        return {"Authorization": f"Bearer {self._token}"}

    async def _get(self, path: str) -> Any:
        try:
            resp = await self._session.get(
                f"{self._base}{path}",
                headers=self._headers(),
                timeout=API_TIMEOUT,
            )
        except (aiohttp.ClientError, asyncio.TimeoutError) as err:
            raise SprApiError(f"Error connecting to SPR: {err}") from err

        if resp.status in (401, 403):
            raise SprAuthError("SPR rejected the API token")
        if resp.status >= 400:
            body = await resp.text()
            raise SprApiError(f"SPR API error {resp.status}: {body[:200]}")
        return await resp.json()

    async def probe(self) -> dict[str, Any]:
        """Identify the router (id, hostname, version)."""
        return await self._get("/probe")

    async def get_state(self) -> dict[str, Any]:
        """Full state snapshot: router, traffic, devices."""
        return await self._get("/state")

    async def wake_on_lan(self, mac: str) -> None:
        """Ask the router to emit a Wake-on-LAN magic packet.

        A GET on purpose: it rides the read-only token scope, and the router
        touches no SPR API for it — it just writes a UDP packet to the LAN.
        """
        await self._get(f"/wake?mac={mac}")

    async def listen_events(self, callback) -> None:
        """Long-running SSE listener; invokes callback(event_dict) per event.

        Raises SprApiError/SprAuthError when the stream drops so the caller
        can back off and reconnect.
        """
        try:
            resp = await self._session.get(
                f"{self._base}/events",
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

        if resp.status in (401, 403):
            raise SprAuthError("SPR rejected the API token")
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
