"""Application configuration management."""

from functools import lru_cache
from pathlib import Path
from typing import Optional

from dotenv import load_dotenv
import os


ENV_PATH = Path(__file__).resolve().parent.parent.parent / ".env"


class Settings:
    def __init__(
        self,
        bot_token: str,
        db_url: str,
        debug: bool = False,
        p2c_base_url: Optional[str] = None,
    ) -> None:
        self.bot_token = bot_token
        self.db_url = db_url
        self.debug = debug
        self.p2c_base_url = p2c_base_url


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    load_dotenv(ENV_PATH)
    bot_token = os.getenv("BOT_TOKEN", "")
    db_url = os.getenv("DB_URL") or os.getenv("DATABASE_URL")
    if not db_url:
        db_url = "sqlite+aiosqlite:///./p2c.db"
    debug = os.getenv("DEBUG", "false").lower() in {"1", "true", "yes"}
    p2c_base_url = os.getenv("P2C_BASE_URL")

    if not bot_token:
        raise RuntimeError("BOT_TOKEN is not set. Configure it in .env.")

    return Settings(
        bot_token=bot_token,
        db_url=db_url,
        debug=debug,
        p2c_base_url=p2c_base_url,
    )
