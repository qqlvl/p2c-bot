"""Database engine and session factory."""

from typing import AsyncIterator, Optional

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine

from .config import get_settings

_engine = None
_sessionmaker: Optional[async_sessionmaker[AsyncSession]] = None
_initialized = False


def get_engine():
    """Lazily create and return the async engine."""
    global _engine
    if _engine is None:
        settings = get_settings()
        _engine = create_async_engine(settings.db_url, echo=settings.debug, future=True)
    return _engine


def get_sessionmaker() -> async_sessionmaker[AsyncSession]:
    """Get or create an async sessionmaker."""
    global _sessionmaker
    if _sessionmaker is None:
        _sessionmaker = async_sessionmaker(
            get_engine(), expire_on_commit=False, class_=AsyncSession
        )
    return _sessionmaker


async def get_session() -> AsyncIterator[AsyncSession]:
    """Yield an async session."""
    async_session = get_sessionmaker()
    async with async_session() as session:
        yield session


async def init_db() -> None:
    """Create database tables once."""
    global _initialized
    if _initialized:
        return

    from app.db import models  # noqa: WPS433

    engine = get_engine()
    async with engine.begin() as conn:
        await conn.run_sync(models.Base.metadata.create_all)

    _initialized = True
