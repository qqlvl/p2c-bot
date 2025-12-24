"""Application configuration."""

from functools import lru_cache

from pydantic import BaseSettings


class Settings(BaseSettings):
    BOT_TOKEN: str
    DB_URL: str = "sqlite+aiosqlite:///./p2c.db"

    class Config:
        env_file = ".env"
        env_file_encoding = "utf-8"
        case_sensitive = False


@lru_cache
def get_settings() -> Settings:
    return Settings()
