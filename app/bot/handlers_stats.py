"""Handlers for statistics."""

from aiogram import F, Router, types
from aiogram.filters import Command
from sqlalchemy import select, text
from datetime import datetime, timedelta

from app.bot.keyboards import BTN_STATS
from app.core.db import AsyncSessionLocal
from app.db.models import CryptoAccount, Order, User
from app.bot.db_utils import ensure_orders_schema

stats_router = Router()

PAID_STATUSES = ("paid", "completed", "done")
PERIODS = {
    "day": ("за день", timedelta(days=1)),
    "week": ("за неделю", timedelta(days=7)),
    "month": ("за месяц", timedelta(days=30)),
}


async def _build_user_stats_text(user: User, period_key: str) -> str:
    title, delta = PERIODS.get(period_key, ("за день", timedelta(days=1)))
    since = datetime.utcnow() - delta
    async with AsyncSessionLocal() as session:
        await ensure_orders_schema(session)
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
        f"<b>📊 Статистика {title}</b>\n\n"
        f"Всего завершённых заявок: <b>{count_orders}</b>\n"
        f"Оборот: <b>{float(turnover_fiat):,.2f}</b> ₽\n"
        f"Наша комиссия (по ордерам): <b>{float(total_fee):,.2f}</b> ₽\n"
        f"Средний чек: <b>{avg_check:,.2f}</b> ₽\n"
    )

    return text


async def _query_stats(user_id: int, since: datetime):
    async with AsyncSessionLocal() as session:
        await ensure_orders_schema(session)
        res = await session.execute(
            text(
                """
                SELECT
                  COUNT(*) as cnt,
                  COALESCE(SUM(amount_fiat), 0) as total_amount,
                  COALESCE(AVG(rate), 0) as avg_rate,
                  COALESCE(SUM(reward_amount), 0) as total_reward
                FROM orders
                WHERE user_id = :user_id
                  AND status IN ('paid','completed','done')
                  AND created_at >= :since
                """
            ),
            {"user_id": user_id, "since": since},
        )
        row = res.one()
        return row


async def _build_stats_text_raw(user: User, period_key: str) -> str:
    title, delta = PERIODS.get(period_key, ("за день", timedelta(days=1)))
    since = datetime.utcnow() - delta
    cnt, total_amount, avg_rate, total_reward = await _query_stats(user.id, since)
    if cnt == 0:
        return f"За выбранный период ({title}) пока нет завершённых заявок."
    avg_check = float(total_amount) / cnt if cnt else 0
    return (
        f"<b>📊 Статистика {title}</b>\n\n"
        f"Заявок: <b>{cnt}</b>\n"
        f"Оборот: <b>{float(total_amount):,.2f}</b> ₽\n"
        f"Средний курс: <b>{float(avg_rate):,.4f}</b>\n"
        f"Средний чек: <b>{avg_check:,.2f}</b> ₽\n"
        f"Вознаграждения всего: <b>{float(total_reward):,.4f}</b>\n"
    )


async def _handle_stats(message: types.Message) -> None:
    from_user = message.from_user

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))

    if user is None:
        await message.answer("Сначала напиши /start, чтобы я тебя запомнил.")
        return

    kb = types.InlineKeyboardMarkup(
        inline_keyboard=[
            [
                types.InlineKeyboardButton(text="📅 День", callback_data="stats:day"),
                types.InlineKeyboardButton(text="🗓 Неделя", callback_data="stats:week"),
                types.InlineKeyboardButton(text="📆 Месяц", callback_data="stats:month"),
            ]
        ]
    )
    await message.answer("Выбери период для статистики:", reply_markup=kb)


@stats_router.message(Command("stats"))
async def cmd_stats(message: types.Message) -> None:
    await _handle_stats(message)


@stats_router.message(F.text == BTN_STATS)
async def btn_stats(message: types.Message) -> None:
    await _handle_stats(message)


@stats_router.callback_query(F.data.startswith("stats:"))
async def stats_period(callback: types.CallbackQuery) -> None:
    period = (callback.data or "").split(":", 1)[1] if ":" in (callback.data or "") else "day"
    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == callback.from_user.id))
    if user is None:
        await callback.answer("Сначала /start", show_alert=True)
        return
    text = await _build_stats_text_raw(user, period)
    try:
        await callback.message.edit_text(text)
    except Exception:
        await callback.message.answer(text)
    await callback.answer()
