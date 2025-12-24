"""P2C API client stub."""

from typing import Any, Awaitable, Callable


class P2CClient:
    def __init__(self, access_token: str) -> None:
        self.access_token = access_token

    async def list_orders(self) -> list[dict[str, Any]]:
        """Return list of orders (stub)."""
        return []

    async def subscribe_new_orders(self, callback: Callable[[dict[str, Any]], Awaitable[None]]) -> None:
        """Subscribe to new orders (stub)."""
        # TODO: implement subscription logic
        return None

    async def take_order(self, order_id: str) -> dict[str, Any]:
        """Take an order (stub)."""
        return {"order_id": order_id, "status": "pending"}
