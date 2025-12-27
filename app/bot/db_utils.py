"""DB helpers for migrations/compat."""

from sqlalchemy import text


async def ensure_orders_schema(session) -> None:
    """Add missing columns to orders table so stats can work."""
    res = await session.execute(text("PRAGMA table_info(orders)"))
    cols = {row[1] for row in res.fetchall()}
    alters: list[str] = []
    if "external_id" not in cols:
        alters.append("ALTER TABLE orders ADD COLUMN external_id TEXT")
    if "account_id" not in cols:
        alters.append("ALTER TABLE orders ADD COLUMN account_id INTEGER")
    if "amount_fiat" not in cols:
        alters.append("ALTER TABLE orders ADD COLUMN amount_fiat REAL")
    if "rate" not in cols:
        alters.append("ALTER TABLE orders ADD COLUMN rate REAL")
    if "reward_amount" not in cols:
        alters.append("ALTER TABLE orders ADD COLUMN reward_amount REAL")
    for stmt in alters:
        await session.execute(text(stmt))
        await session.commit()


def wei_to_float(val: str) -> float:
    try:
        return float(val) / 1e18
    except Exception:
        return 0.0
