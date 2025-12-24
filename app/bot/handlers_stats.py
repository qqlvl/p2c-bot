"""Handlers for statistics."""

from aiogram import F, Router, types
from aiogram.filters import Command
from sqlalchemy import func, select

from app.bot.keyboards import BTN_STATS
from app.core.db import AsyncSessionLocal
from app.db.models import CryptoAccount, Order, User

stats_router = Router()

PAID_STATUSES = ("paid", "completed", "done")


async def _build_user_stats_text(user: User) -> str:
    async with AsyncSessionLocal() as session:
        stmt_acc = select(CryptoAccount.id).where(CryptoAccount.user_id == user.id)
        res_acc = await session.execute(stmt_acc)
        account_ids = [row[0] for row in res_acc.all()]

        if not account_ids:
            return (
                "У тебя пока нет подключённых аккаунтов, поэтому статистики нет.\n"
                "Нажми «➕ Подключить аккаунт» и подключи первый."
            )

        stmt_stats = (
            select(
                func.count(Order.id),
                func.coalesce(func.sum(Order.amount_fiat), 0),
                func.coalesce(func.sum(Order.our_fee_amount), 0),
            )
            .where(
                Order.account_id.in_(account_ids),
                Order.status.in_(PAID_STATUSES),
            )
        )

        res_stats = await session.execute(stmt_stats)
        count_orders, turnover_fiat, total_fee = res_stats.one()

    if count_orders == 0:
        return (
            "Пока нет ни одной завершённой заявки 💤\n"
            "Как только начнёшь принимать оплаты — здесь появится статистика."
        )

    avg_check = float(turnover_fiat) / count_orders if count_orders else 0

    text = (
        "<b>📊 Статистика по всем аккаунтам</b>\n\n"
        f"Всего завершённых заявок: <b>{count_orders}</b>\n"
        f"Оборот: <b>{float(turnover_fiat):,.2f}</b> ₽\n"
        f"Наша комиссия (по ордерам): <b>{float(total_fee):,.2f}</b> ₽\n"
        f"Средний чек: <b>{avg_check:,.2f}</b> ₽\n"
    )

    return text


async def _handle_stats(message: types.Message) -> None:
    from_user = message.from_user

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))

    if user is None:
        await message.answer("Сначала напиши /start, чтобы я тебя запомнил.")
        return

    text = await _build_user_stats_text(user)
    await message.answer(text)


@stats_router.message(Command("stats"))
async def cmd_stats(message: types.Message) -> None:
    await _handle_stats(message)


@stats_router.message(F.text == BTN_STATS)
async def btn_stats(message: types.Message) -> None:
    await _handle_stats(message)
