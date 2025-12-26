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

    async def reload_account(
        self,
        account_id: int,
        access_token: str | None = None,
        chat_id: int | None = None,
        min_amount: float | None = None,
        max_amount: float | None = None,
        auto_mode: bool | None = None,
        is_active: bool | None = None,
        p2c_account_id: str | None = None,
    ) -> bool:
        url = self._build_url("/accounts/reload")
        if not url:
            return False
        payload: dict[str, object] = {"account_id": account_id}
        if access_token:
            payload["access_token"] = access_token
        if chat_id is not None:
            payload["chat_id"] = chat_id
        if min_amount is not None:
            payload["min_amount"] = min_amount
        if max_amount is not None:
            payload["max_amount"] = max_amount
        if auto_mode is None:
            auto_mode = True
        if is_active is None:
            is_active = True
        payload["auto_mode"] = auto_mode
        payload["is_active"] = is_active
        if p2c_account_id:
            payload["p2c_account_id"] = p2c_account_id
        async with httpx.AsyncClient(timeout=2.0) as client:
            try:
                resp = await client.post(url, json=payload)
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

    async def complete_order(self, account_id: int, payment_id: str) -> bool:
        url = self._build_url("/orders/complete")
        if not url:
            return False
        payload = {"account_id": account_id, "payment_id": payment_id}
        async with httpx.AsyncClient(timeout=2.0) as client:
            try:
                resp = await client.post(url, json=payload)
                resp.raise_for_status()
                data = resp.json()
                return bool(data.get("ok", True))
            except httpx.HTTPError:
                return False


engine_client = P2CEngineClient()
