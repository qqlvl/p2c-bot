"""SQLAlchemy models."""

from datetime import datetime

from sqlalchemy import BigInteger, Boolean, DateTime, ForeignKey, Numeric, String
from sqlalchemy.orm import Mapped, declarative_base, mapped_column, relationship

Base = declarative_base()


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    telegram_id: Mapped[int] = mapped_column(BigInteger, unique=True, index=True)

    username: Mapped[str | None] = mapped_column(String(255), nullable=True)
    first_name: Mapped[str | None] = mapped_column(String(255), nullable=True)

    is_active: Mapped[bool] = mapped_column(Boolean, default=True)

    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )

    accounts: Mapped[list["CryptoAccount"]] = relationship(
        back_populates="user", cascade="all, delete-orphan", lazy="selectin"
    )
    orders: Mapped[list["Order"]] = relationship(
        back_populates="user", cascade="all, delete-orphan", lazy="selectin"
    )


class CryptoAccount(Base):
    __tablename__ = "crypto_accounts"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    name: Mapped[str | None] = mapped_column(String(255))
    access_token_enc: Mapped[str | None] = mapped_column(String(1024))
    notification_chat_id: Mapped[int | None] = mapped_column(BigInteger, index=True)
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, default=datetime.utcnow
    )
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )

    user: Mapped[User] = relationship(back_populates="accounts")
    settings: Mapped["AccountSettings"] = relationship(
        back_populates="account", uselist=False, cascade="all, delete-orphan", lazy="selectin"
    )


class AccountSettings(Base):
    __tablename__ = "account_settings"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    account_id: Mapped[int] = mapped_column(
        ForeignKey("crypto_accounts.id"), unique=True, index=True
    )
    notifications_enabled: Mapped[bool] = mapped_column(default=True)
    min_amount_fiat: Mapped[float | None] = mapped_column(Numeric(18, 2), nullable=True)
    max_amount_fiat: Mapped[float | None] = mapped_column(Numeric(18, 2), nullable=True)
    auto_mode: Mapped[bool] = mapped_column(Boolean, default=False)
    created_at: Mapped[datetime] = mapped_column(
        DateTime, default=datetime.utcnow
    )

    account: Mapped[CryptoAccount] = relationship(back_populates="settings")


class Order(Base):
    __tablename__ = "orders"

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    external_id: Mapped[str] = mapped_column(String(128), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    account_id: Mapped[int] = mapped_column(ForeignKey("crypto_accounts.id"), index=True)

    amount_fiat: Mapped[float | None] = mapped_column(Numeric(18, 2), nullable=True)
    fiat_currency: Mapped[str | None] = mapped_column(String(10), nullable=True)

    amount_crypto: Mapped[float | None] = mapped_column(Numeric(18, 8), nullable=True)
    crypto_currency: Mapped[str | None] = mapped_column(String(10), nullable=True)

    rate: Mapped[float | None] = mapped_column(Numeric(18, 6), nullable=True)

    status: Mapped[str | None] = mapped_column(String(32), index=True)
    our_fee_percent: Mapped[float | None] = mapped_column(Numeric(5, 2), default=2.0)
    our_fee_amount: Mapped[float | None] = mapped_column(Numeric(18, 2), nullable=True)

    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)
    received_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)
    updated_at: Mapped[datetime] = mapped_column(
        DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )

    user: Mapped[User] = relationship(back_populates="orders")
