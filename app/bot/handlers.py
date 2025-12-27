"""Bot handlers."""

from aiogram import F, Router, types
from aiogram.filters import Command, CommandStart
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from aiogram.types import InlineKeyboardButton, InlineKeyboardMarkup
from aiogram.exceptions import TelegramBadRequest
from sqlalchemy import delete, func, select, text
from datetime import datetime

from app.bot.keyboards import (
    BTN_ADD_ACCOUNT,
    BTN_LIST_ACCOUNTS,
    BTN_STATS,
    main_menu_kb,
)
from app.core.config import get_settings
from app.core.db import AsyncSessionLocal
from app.db.models import AccountSettings, CryptoAccount, Order, User
from app.services.engine_client import engine_client
import httpx
from sqlalchemy.exc import SQLAlchemyError
from app.bot.db_utils import ensure_orders_schema, wei_to_float


def build_default_payment_kb(acc_id: int, payment_id: str, amount: float, rate: float, fee: float) -> InlineKeyboardMarkup:
    payload = f"paid:{acc_id}:{payment_id}:{amount}:{rate}:{fee}"
    cancel_payload = f"cancel:{acc_id}:{payment_id}"
    return InlineKeyboardMarkup(
        inline_keyboard=[
            [
                InlineKeyboardButton(text="✅ Я оплатил", callback_data=payload),
                InlineKeyboardButton(text="❌ Отменить", callback_data=cancel_payload),
            ]
        ]
    )


def build_confirm_kb(prefix: str, ok_payload: str, back_payload: str) -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(
        inline_keyboard=[
            [
                InlineKeyboardButton(text="Да", callback_data=f"{prefix}ok:{ok_payload}"),
                InlineKeyboardButton(text="↩️ Назад", callback_data=f"{prefix}back:{back_payload}"),
            ]
        ]
    )


async def refresh_account_view(callback: types.CallbackQuery, acc_id: int) -> None:
    # Re-render account menu by reusing selection logic.
    fake_cb = types.CallbackQuery(
        id=callback.id,
        from_user=callback.from_user,
        chat_instance=callback.chat_instance,
        data=f"acc:{acc_id}",
        message=callback.message,
    )
    await on_account_selected(fake_cb)


async def _engine_reload(
    account_id: int,
    access_token: str | None,
    *,
    chat_id: int | None = None,
    min_amount: float | None = None,
    max_amount: float | None = None,
    auto_mode: bool | None = None,
    is_active: bool | None = None,
    p2c_account_id: str | None = None,
) -> None:
    if min_amount is not None:
        min_amount = float(min_amount)
    if max_amount is not None:
        max_amount = float(max_amount)
    await engine_client.reload_account(
        account_id=account_id,
        access_token=access_token,
        chat_id=chat_id,
        min_amount=min_amount,
        max_amount=max_amount,
        auto_mode=auto_mode,
        is_active=is_active,
        p2c_account_id=p2c_account_id,
    )

router = Router()

# ... existing handlers ...


@router.callback_query(F.data.startswith("paid:"))
async def on_paid(callback: types.CallbackQuery) -> None:
    """Подтверждение оплаты по кнопке из уведомления."""
    parts = (callback.data or "").split(":")
    # expected: paid:<acc_id>:<payment_id>:<amount>:<rate>:<fee>
    if len(parts) < 6:
        await callback.answer("Не распознал данные платежа", show_alert=True)
        return
    try:
        acc_id = int(parts[1])
        payment_id = parts[2]
        amount = float(parts[3])
        rate = float(parts[4])
        fee = float(parts[5])
    except (ValueError, IndexError):
        await callback.answer("Ошибка данных платежа", show_alert=True)
        return

    # Первая кнопка → показываем подтверждение.
    await callback.answer("Подтвердить оплату?", show_alert=False)
    ok_payload = f"{acc_id}:{payment_id}:{amount}:{rate}:{fee}"
    kb = build_confirm_kb("paid_", ok_payload, ok_payload)
    try:
        await callback.message.edit_reply_markup(reply_markup=kb)
    except Exception:
        pass


@router.callback_query(F.data.startswith("paid_ok:"))
async def on_paid_ok(callback: types.CallbackQuery) -> None:
    parts = (callback.data or "").split(":")
    if len(parts) < 6:
        await callback.answer("Не распознал данные платежа", show_alert=True)
        return
    try:
        acc_id = int(parts[1])
        payment_id = parts[2]
        amount = float(parts[3])
        rate = float(parts[4])
        fee = float(parts[5])
    except (ValueError, IndexError):
        await callback.answer("Ошибка данных платежа", show_alert=True)
        return

    ok = await engine_client.complete_order(acc_id, payment_id)
    if not ok:
        await callback.answer("Не удалось подтвердить оплату на стороне P2C", show_alert=True)
        return

    async with AsyncSessionLocal() as session:
        await ensure_orders_schema(session)
        account = await session.scalar(
            select(CryptoAccount).where(CryptoAccount.id == acc_id)
        )
        if account is None:
            await callback.answer("Аккаунт не найден", show_alert=True)
            return
        user_id = account.user_id
        reward = fee
        try:
            await session.execute(
                text(
                    """
                    INSERT INTO orders (user_id, account_id, external_id, status, amount, amount_fiat, rate, reward_amount, created_at)
                    VALUES (:user_id, :account_id, :external_id, :status, :amount, :amount_fiat, :rate, :reward_amount, :created_at)
                    """
                ),
                {
                    "user_id": user_id,
                    "account_id": acc_id,
                    "external_id": payment_id,
                    "status": "paid",
                    "amount": amount,
                    "amount_fiat": amount,
                    "rate": rate,
                    "reward_amount": reward,
                    "created_at": datetime.utcnow(),
                },
            )
            await session.commit()
        except Exception:
            await session.rollback()

    # Обновляем сообщение
    try:
        caption = callback.message.caption or ""
        caption = caption + "\n\n✅ Оплата подтверждена."
        await callback.message.edit_caption(caption, reply_markup=None)
    except Exception:
        try:
            await callback.message.edit_text("✅ Оплата подтверждена.", reply_markup=None)
        except Exception:
            pass
    await callback.answer("✅ Отметил как оплачено.", show_alert=False)


@router.callback_query(F.data.startswith("paid_back:"))
async def on_paid_back(callback: types.CallbackQuery) -> None:
    parts = (callback.data or "").split(":")
    if len(parts) < 6:
        await callback.answer()
        return
    try:
        acc_id = int(parts[1])
        payment_id = parts[2]
        amount = float(parts[3])
        rate = float(parts[4])
        fee = float(parts[5])
    except Exception:
        await callback.answer()
        return
    kb = build_default_payment_kb(acc_id, payment_id, amount, rate, fee)
    try:
        await callback.message.edit_reply_markup(reply_markup=kb)
    except Exception:
        pass
    await callback.answer()


@router.callback_query(F.data.startswith("cancel:"))
async def on_cancel(callback: types.CallbackQuery) -> None:
    """Отмена заявки из уведомления."""
    parts = (callback.data or "").split(":")
    # expected: cancel:<acc_id>:<payment_id>
    if len(parts) < 3:
        await callback.answer("Не распознал заявку", show_alert=True)
        return
    try:
        acc_id = int(parts[1])
        payment_id = parts[2]
    except (ValueError, IndexError):
        await callback.answer("Ошибка данных заявки", show_alert=True)
        return

    await callback.answer("Точно отменить заявку?", show_alert=False)
    # amount/rate/fee неизвестны здесь, поэтому ставим заглушки для возврата (0).
    back_payload = f"{acc_id}:{payment_id}:0:0:0"
    kb = build_confirm_kb("cancel_", f"{acc_id}:{payment_id}", back_payload)
    try:
        await callback.message.edit_reply_markup(reply_markup=kb)
    except Exception:
        pass


@router.callback_query(F.data.startswith("cancel_ok:"))
async def on_cancel_ok(callback: types.CallbackQuery) -> None:
    parts = (callback.data or "").split(":")
    if len(parts) < 3:
        await callback.answer("Не распознал заявку", show_alert=True)
        return
    try:
        acc_id = int(parts[1])
        payment_id = parts[2]
    except Exception:
        await callback.answer("Ошибка данных заявки", show_alert=True)
        return

    ok = await engine_client.cancel_order(acc_id, payment_id)
    if not ok:
        await callback.answer("Не удалось отменить заявку на стороне P2C", show_alert=True)
        return

    # Удаляем сообщение с QR, чтобы не висело в чате
    try:
        await callback.message.delete()
    except Exception:
        try:
            caption = callback.message.caption or ""
            caption = caption + "\n\n❌ Заявка отменена."
            await callback.message.edit_caption(caption, reply_markup=None)
        except Exception:
            pass
    await callback.answer("❌ Заявка отменена.", show_alert=False)


@router.callback_query(F.data.startswith("cancel_back:"))
async def on_cancel_back(callback: types.CallbackQuery) -> None:
    parts = (callback.data or "").split(":")
    if len(parts) < 6:
        await callback.answer()
        return
    try:
        acc_id = int(parts[1])
        payment_id = parts[2]
        amount = float(parts[3])
        rate = float(parts[4])
        fee = float(parts[5])
    except Exception:
        await callback.answer()
        return
    kb = build_default_payment_kb(acc_id, payment_id, amount, rate, fee)
    try:
        await callback.message.edit_reply_markup(reply_markup=kb)
    except Exception:
        pass
    await callback.answer()


class AddAccount(StatesGroup):
    waiting_token = State()
    waiting_name = State()


class FilterAmount(StatesGroup):
    waiting_min = State()
    waiting_max = State()


class EditAssets(StatesGroup):
    pass


async def _get_or_create_user(session, from_user: types.User) -> User:
    user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
    if user is None:
        user = User(
            telegram_id=from_user.id,
            username=from_user.username,
            first_name=from_user.first_name,
        )
        session.add(user)
    else:
        user.username = from_user.username
        user.first_name = from_user.first_name
    return user


async def _ensure_p2c_account_map_table(session) -> None:
    await session.execute(
        text(
            """
            CREATE TABLE IF NOT EXISTS p2c_account_map (
                account_id INTEGER PRIMARY KEY,
                p2c_account_id TEXT
            )
            """
        )
    )


async def _get_or_fetch_p2c_account_id(
    session, account_id: int, access_token: str
) -> str | None:
    await _ensure_p2c_account_map_table(session)
    res = await session.execute(
        text(
            "SELECT p2c_account_id FROM p2c_account_map WHERE account_id = :account_id"
        ),
        {"account_id": account_id},
    )
    row = res.first()
    if row and row[0]:
        return row[0]

    # fetch from P2C
    try:
        async with httpx.AsyncClient(timeout=3.0) as client:
            resp = await client.get(
                "https://app.cr.bot/internal/v1/p2c/accounts",
                headers={"Cookie": f"access_token={access_token}"},
            )
            resp.raise_for_status()
            data = resp.json().get("data") or []
            if not data:
                return None
            p2c_id = data[0].get("id")
            if p2c_id:
                await session.execute(
                    text(
                        "INSERT OR REPLACE INTO p2c_account_map (account_id, p2c_account_id) "
                        "VALUES (:account_id, :p2c_account_id)"
                    ),
                    {"account_id": account_id, "p2c_account_id": p2c_id},
                )
                await session.commit()
            return p2c_id
    except (httpx.HTTPError, SQLAlchemyError):
        await session.rollback()
        return None


@router.message(CommandStart())
async def start(message: types.Message, state: FSMContext) -> None:
    await state.clear()
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        return

    async with AsyncSessionLocal() as session:
        await _get_or_create_user(session, from_user)
        await session.commit()

    await message.answer(
        "Используй кнопки ниже, чтобы начать:",
        reply_markup=main_menu_kb,
    )


async def _start_add_account_flow(message: types.Message, state: FSMContext) -> None:
    await state.set_state(AddAccount.waiting_token)
    await message.answer(
        "Пришли мне <b>access token</b> от твоего P2C/CryptoBot аккаунта.\n\n"
        "Я сохраню его и буду использовать для ловли заявок.\n"
        "Если передумаешь — просто не отправляй токен и напиши /cancel.",
        reply_markup=main_menu_kb,
    )


async def _show_accounts_inline(message: types.Message) -> None:
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        return

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
        if user is None:
            await message.answer("Сначала напиши /start, чтобы зарегистрироваться.")
            return

        accounts_iter = await session.scalars(
            select(CryptoAccount).where(CryptoAccount.user_id == user.id)
        )
        accounts_list = list(accounts_iter)

    if not accounts_list:
        await message.answer(
            "У тебя пока нет подключённых аккаунтов.\n"
            "Нажми «➕ Подключить аккаунт», чтобы привязать первый.",
            reply_markup=main_menu_kb,
        )
        return

    buttons = []
    for acc in accounts_list:
        text = f"{acc.name or 'Без названия'} (id={acc.id})"
        buttons.append(
            [InlineKeyboardButton(text=text, callback_data=f"acc:{acc.id}")]
        )

    kb = InlineKeyboardMarkup(inline_keyboard=buttons)

    await message.answer(
        "Выбери аккаунт, с которым хочешь работать 👇",
        reply_markup=kb,
    )


@router.message(Command("add_account"))
@router.message(F.text == BTN_ADD_ACCOUNT)
@router.message(F.text.lower() == "подключить аккаунт")
async def add_account(message: types.Message, state: FSMContext) -> None:
    await _start_add_account_flow(message, state)


@router.message(Command("accounts"))
@router.message(F.text == BTN_LIST_ACCOUNTS)
async def accounts(message: types.Message) -> None:
    await _show_accounts_inline(message)


@router.message(AddAccount.waiting_token)
async def receive_account_token(message: types.Message, state: FSMContext) -> None:
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        await state.clear()
        return

    token = message.text
    if not token or len(token.strip()) < 10:
        await message.answer("Похоже, это не токен. Пришли строку целиком.")
        return
    token = token.strip()

    await state.update_data(access_token=token)
    await state.set_state(AddAccount.waiting_name)
    await message.answer(
        "Как назвать этот аккаунт? Напиши имя или пришлю дефолтное.",
        reply_markup=main_menu_kb,
    )


@router.message(AddAccount.waiting_name)
async def receive_account_name(message: types.Message, state: FSMContext) -> None:
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        await state.clear()
        return

    data = await state.get_data()
    token = data.get("access_token")
    if not token:
        await message.answer("Не вижу токен. Начни заново командой /add_account.")
        await state.clear()
        return

    provided_name = (message.text or "").strip()

    async with AsyncSessionLocal() as session:
        user = await _get_or_create_user(session, from_user)
        count = await session.scalar(
            select(func.count(CryptoAccount.id)).where(CryptoAccount.user_id == user.id)
        )
        default_name = f"Account #{(count or 0) + 1}"
        account_name = provided_name or default_name

        account = CryptoAccount(
            user=user,
            name=account_name,
            access_token_enc=token,
            notification_chat_id=from_user.id,
            is_active=True,
        )
        session.add(account)
        await session.commit()
        # fetch p2c account id
        p2c_acc_id = await _get_or_fetch_p2c_account_id(session, account.id, account.access_token_enc)
        await _engine_reload(
            account.id,
            account.access_token_enc,
            chat_id=account.notification_chat_id,
            min_amount=None,
            max_amount=None,
            auto_mode=False,  # не стартуем приём, пока юзер не включит сам
            is_active=account.is_active,
            p2c_account_id=p2c_acc_id,
        )

    await state.clear()
    await message.answer(
        f"✅ Аккаунт {account_name} подключён.\n\n"
        "Теперь я смогу использовать его, чтобы ловить QR.",
        reply_markup=main_menu_kb,
    )


@router.message(Command("my_accounts"))
async def my_accounts(message: types.Message) -> None:
    await accounts(message)


@router.callback_query(F.data.startswith("acc:"))
async def on_account_selected(callback: types.CallbackQuery) -> None:
    data = callback.data or ""
    _, acc_id_str = data.split(":", 1)
    acc_id = int(acc_id_str)

    from_user = callback.from_user
    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
        if user is None:
            await callback.answer("Сначала /start", show_alert=True)
            return
        account = await session.scalar(
            select(CryptoAccount).where(
                CryptoAccount.id == acc_id, CryptoAccount.user_id == user.id
            )
        )
        settings = None
        if account is not None:
            settings = await session.scalar(
                select(AccountSettings).where(AccountSettings.account_id == acc_id)
            )

    if account is None:
        await callback.answer("Аккаунт не найден", show_alert=True)
        return

    auto_on = settings.auto_mode if settings else False
    toggle_text = "🟢 Принимать заявки" if auto_on else "🔴 Не принимать заявки"
    min_val = settings.min_amount_fiat if settings else None
    max_val = settings.max_amount_fiat if settings else None
    filt_parts = []
    filt_parts.append(f"мин: {min_val}" if min_val is not None else "мин: нет")
    filt_parts.append(f"макс: {max_val}" if max_val is not None else "макс: нет")
    filter_text = ", ".join(filt_parts)
    active_status = "🟢 Активен" if account.is_active else "⚪️ Выключен"

    kb = InlineKeyboardMarkup(
        inline_keyboard=[
            [
                InlineKeyboardButton(
                    text="🎚 Фильтр по сумме",
                    callback_data=f"accf:{acc_id}",
                )
            ],
            [
                InlineKeyboardButton(
                    text=toggle_text,
                    callback_data=f"accauto:{acc_id}",
                )
            ],
            [
                InlineKeyboardButton(
                    text="💱 Активировать/выключить",
                    callback_data=f"accact:{acc_id}",
                )
            ],
            [
                InlineKeyboardButton(
                    text="🗑 Удалить аккаунт",
                    callback_data=f"accdel:{acc_id}",
                )
            ],
            [
                InlineKeyboardButton(
                    text="⬅️ Назад",
                    callback_data="acc_back",
                )
            ],
        ]
    )

    await callback.message.edit_text(
        f"Аккаунт <b>{account.name or account.id}</b>\n"
        f"{active_status}\n"
        f"Фильтр: {filter_text}\n"
        f"Активен: {'да' if account.is_active else 'нет'}\n"
        f"Принимать заявки: {'да' if auto_on else 'нет'}\n"
        "Что хочешь сделать?",
        reply_markup=kb,
    )
    if getattr(callback, "bot", None):
        try:
            await callback.answer()
        except TelegramBadRequest:
            # Callback may be too old; ignore.
            pass


@router.callback_query(F.data == "acc_back")
async def on_accounts_back(callback: types.CallbackQuery) -> None:
    await _show_accounts_inline(callback.message)
    await callback.answer()


@router.callback_query(F.data.startswith("accf:"))
async def on_account_filter(callback: types.CallbackQuery, state: FSMContext) -> None:
    _, acc_id_str = (callback.data or "").split(":", 1)
    await state.update_data(account_id=int(acc_id_str))
    await state.set_state(FilterAmount.waiting_min)
    await callback.answer()
    await callback.message.answer(
        "Введи минимальную сумму в фиате (например, 1500.00). 0 — без нижней границы.",
        reply_markup=main_menu_kb,
    )


@router.message(FilterAmount.waiting_min)
async def on_filter_amount_min(message: types.Message, state: FSMContext) -> None:
    text_value = (message.text or "").replace(",", ".").strip()
    try:
        amount = float(text_value)
        if amount < 0:
            raise ValueError
    except ValueError:
        await message.answer("Нужно число, например 1500.00 или 0. Попробуй снова.")
        return

    await state.update_data(min_amount=amount)
    await state.set_state(FilterAmount.waiting_max)
    await message.answer(
        "Теперь введи максимальную сумму (0 — без верхнего лимита).",
        reply_markup=main_menu_kb,
    )


@router.message(FilterAmount.waiting_max)
async def on_filter_amount_max(message: types.Message, state: FSMContext) -> None:
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        await state.clear()
        return

    data = await state.get_data()
    acc_id = data.get("account_id")
    min_amount = data.get("min_amount", 0)
    if acc_id is None:
        await message.answer("Не вижу выбранный аккаунт. Начни заново через /accounts.")
        await state.clear()
        return

    text_value = (message.text or "").replace(",", ".").strip()
    try:
        max_amount = float(text_value)
        if max_amount < 0:
            raise ValueError
    except ValueError:
        await message.answer("Нужно число, например 2500.00 или 0. Попробуй снова.")
        return

    # Interpret 0 as no limit.
    min_val = None if min_amount == 0 else min_amount
    max_val = None if max_amount == 0 else max_amount
    if min_val is not None and max_val is not None and max_val < min_val:
        await message.answer("Максимум не может быть меньше минимума. Попробуй снова.")
        return

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
        if user is None:
            await message.answer("Сначала напиши /start.")
            await state.clear()
            return

        account = await session.scalar(
            select(CryptoAccount).where(
                CryptoAccount.id == acc_id, CryptoAccount.user_id == user.id
            )
        )
        if account is None:
            await message.answer("Аккаунт не найден. Начни заново через /accounts.")
            await state.clear()
            return

        settings = await session.scalar(
            select(AccountSettings).where(AccountSettings.account_id == acc_id)
        )
        if settings is None:
            settings = AccountSettings(account_id=acc_id)
            session.add(settings)
        settings.min_amount_fiat = min_val
        settings.max_amount_fiat = max_val
        await session.commit()

    await state.clear()
    await message.answer(
        f"Фильтр для {account.name or account.id} сохранён:\n"
        f"мин: {min_val if min_val is not None else 'нет'}, "
        f"макс: {max_val if max_val is not None else 'нет'}",
        reply_markup=main_menu_kb,
    )
    async with AsyncSessionLocal() as session:
        p2c_acc = await _get_or_fetch_p2c_account_id(session, acc_id, account.access_token_enc or "")
    await _engine_reload(
        acc_id,
        account.access_token_enc,
        chat_id=account.notification_chat_id,
        min_amount=min_val,
        max_amount=max_val,
        auto_mode=settings.auto_mode if settings is not None else False,
        is_active=account.is_active,
        p2c_account_id=p2c_acc,
    )


@router.callback_query(F.data.startswith("accdel:"))
async def on_account_delete(callback: types.CallbackQuery) -> None:
    _, acc_id_str = (callback.data or "").split(":", 1)
    acc_id = int(acc_id_str)
    kb = InlineKeyboardMarkup(
        inline_keyboard=[
            [
                InlineKeyboardButton(
                    text="✅ Удалить",
                    callback_data=f"accdelok:{acc_id}",
                ),
                InlineKeyboardButton(text="⬅️ Отмена", callback_data="acc_back"),
            ]
        ]
    )
    await callback.message.edit_text(
        f"Удалить аккаунт ID {acc_id}? Это действие необратимо.",
        reply_markup=kb,
    )
    await callback.answer()


@router.callback_query(F.data.startswith("accdelok:"))
async def on_account_delete_confirm(callback: types.CallbackQuery) -> None:
    _, acc_id_str = (callback.data or "").split(":", 1)
    acc_id = int(acc_id_str)
    from_user = callback.from_user

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
        if user is None:
            await callback.answer("Сначала /start", show_alert=True)
            return

        account = await session.scalar(
            select(CryptoAccount).where(
                CryptoAccount.id == acc_id, CryptoAccount.user_id == user.id
            )
        )
        if account is None:
            await callback.answer("Аккаунт не найден", show_alert=True)
            return

        await session.execute(delete(Order).where(Order.account_id == acc_id))
        await session.delete(account)
        await session.commit()

    await callback.message.answer(f"Аккаунт ID {acc_id} удалён.")
    await callback.answer()
    await _show_accounts_inline(callback.message)
    await _engine_reload(acc_id, None, auto_mode=False, is_active=False)


@router.callback_query(F.data.startswith("accact:"))
async def on_account_toggle_active(callback: types.CallbackQuery) -> None:
    _, acc_id_str = (callback.data or "").split(":", 1)
    acc_id = int(acc_id_str)
    from_user = callback.from_user

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
        if user is None:
            await callback.answer("Сначала /start", show_alert=True)
            return

        account = await session.scalar(
            select(CryptoAccount).where(
                CryptoAccount.id == acc_id, CryptoAccount.user_id == user.id
            )
        )
        settings = await session.scalar(
            select(AccountSettings).where(AccountSettings.account_id == acc_id)
        )
        if account is None:
            await callback.answer("Аккаунт не найден", show_alert=True)
            return

        account.is_active = not account.is_active
        await session.commit()
        status = "активирован" if account.is_active else "выключен"

    await callback.answer(f"Аккаунт {status}.")
    await refresh_account_view(callback, acc_id)
    async with AsyncSessionLocal() as session:
        p2c_acc = await _get_or_fetch_p2c_account_id(session, acc_id, account.access_token_enc or "")
    await _engine_reload(
        acc_id,
        account.access_token_enc,
        chat_id=account.notification_chat_id,
        min_amount=settings.min_amount_fiat if settings else None,
        max_amount=settings.max_amount_fiat if settings else None,
        auto_mode=settings.auto_mode if settings else False,
        is_active=account.is_active,
        p2c_account_id=p2c_acc,
    )




@router.callback_query(F.data.startswith("accauto:"))
async def on_account_auto_toggle(callback: types.CallbackQuery) -> None:
    _, acc_id_str = (callback.data or "").split(":", 1)
    acc_id = int(acc_id_str)
    from_user = callback.from_user

    async with AsyncSessionLocal() as session:
        user = await session.scalar(select(User).where(User.telegram_id == from_user.id))
        if user is None:
            await callback.answer("Сначала /start", show_alert=True)
            return

        account = await session.scalar(
            select(CryptoAccount).where(
                CryptoAccount.id == acc_id, CryptoAccount.user_id == user.id
            )
        )
        if account is None:
            await callback.answer("Аккаунт не найден", show_alert=True)
            return

        settings = await session.scalar(
            select(AccountSettings).where(AccountSettings.account_id == acc_id)
        )
        if settings is None:
            settings = AccountSettings(account_id=acc_id)
            session.add(settings)
        settings.auto_mode = not settings.auto_mode
        await session.commit()
        new_state = "включен" if settings.auto_mode else "выключен"

    await callback.answer(f"Приём заявок {new_state}.")
    await refresh_account_view(callback, acc_id)
    async with AsyncSessionLocal() as session:
        p2c_acc = await _get_or_fetch_p2c_account_id(session, acc_id, account.access_token_enc or "")
    await _engine_reload(
        acc_id,
        account.access_token_enc,
        chat_id=account.notification_chat_id,
        min_amount=settings.min_amount_fiat if settings else None,
        max_amount=settings.max_amount_fiat if settings else None,
        auto_mode=settings.auto_mode if settings else False,
        is_active=account.is_active,
        p2c_account_id=p2c_acc,
    )
