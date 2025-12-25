"""HTTP client for Go p2c-engine service."""

import httpx

from app.core.config import get_settings


class P2CEngineClient:
    def __init__(self, base_url: str | None = None) -> None:
        settings = get_settings()
        self.base_url = (base_url or settings.ENGINE_URL or "").rstrip("/")

    def _build_url(self, path: str) -> str:
        if not self.base_url:
            return ""
        return f"{self.base_url}{path}"

    async def reload_account(self, account_id: int) -> bool:
        url = self._build_url("/accounts/reload")
        if not url:
            return False
        async with httpx.AsyncClient(timeout=2.0) as client:
            try:
                resp = await client.post(url, json={"account_id": account_id})
                resp.raise_for_status()
                data = resp.json()
                return bool(data.get("ok", True))
            except httpx.HTTPError:
                return False

    async def take_order(self, account_id: int, order_external_id: str) -> bool:
        url = self._build_url("/orders/take")
        if not url:
            return False
        payload = {
            "account_id": account_id,
            "order_external_id": order_external_id,
        }
        async with httpx.AsyncClient(timeout=2.0) as client:
            try:
                resp = await client.post(url, json=payload)
                resp.raise_for_status()
                data = resp.json()
                return bool(data.get("ok", True))
            except httpx.HTTPError:
                return False


engine_client = P2CEngineClient()
