"""Bot handlers."""

from aiogram import F, Router, types
from aiogram.filters import Command, CommandStart
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from sqlalchemy import func, select

from app.bot.keyboards import (
    BTN_ADD_ACCOUNT,
    BTN_LIST_ACCOUNTS,
    main_menu_kb,
)
from app.core.db import AsyncSessionLocal
from app.db.models import CryptoAccount, User

router = Router()


class AddAccount(StatesGroup):
    waiting_token = State()
    waiting_name = State()


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
        "Пришли мне <b>access token</b> от твоего P2C/CryptoBot аккаунта.\n\n",
        reply_markup=main_menu_kb,
    )


async def _show_accounts(message: types.Message) -> None:
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

    lines = []
    for acc in accounts_list:
        status = "🟢 активен" if acc.is_active else "⚪️ выключен"
        lines.append(f"{acc.id}. {acc.name or 'Без названия'} — {status}")

    await message.answer("Твои аккаунты:\n\n" + "\n".join(lines), reply_markup=main_menu_kb)


@router.message(Command("add_account"))
@router.message(F.text == BTN_ADD_ACCOUNT)
@router.message(F.text.lower() == "подключить аккаунт")
async def add_account(message: types.Message, state: FSMContext) -> None:
    await _start_add_account_flow(message, state)


@router.message(Command("accounts"))
@router.message(F.text == BTN_LIST_ACCOUNTS)
async def accounts(message: types.Message) -> None:
    await _show_accounts(message)


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

    await state.clear()
    await message.answer(
        f"✅ Аккаунт {account_name} подключён.\n\n"
        "Теперь я смогу использовать его для ловли заявок.",
        reply_markup=main_menu_kb,
    )


@router.message(Command("my_accounts"))
async def my_accounts(message: types.Message) -> None:
    await accounts(message)
