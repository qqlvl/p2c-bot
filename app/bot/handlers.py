"""Bot handlers."""

from aiogram import F, Router, types
from aiogram.filters import Command, CommandStart
from aiogram.fsm.context import FSMContext
from aiogram.fsm.state import State, StatesGroup
from sqlalchemy import func, select

from app.core.db import get_sessionmaker
from app.db.models import CryptoAccount, User

router = Router()


class AddAccount(StatesGroup):
    waiting_token = State()


async def _get_or_create_user(session, from_user: types.User) -> User:
    user = await session.scalar(select(User).where(User.tg_id == from_user.id))
    if user is None:
        user = User(
            tg_id=from_user.id,
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
    sessionmaker = get_sessionmaker()
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        return

    async with sessionmaker() as session:
        user = await _get_or_create_user(session, from_user)
        await session.commit()

    await message.answer(
        "Привет! Я помогу подключить P2C. Нажми /add_account или пришли команду "
        "«Подключить аккаунт», чтобы добавить токен."
    )


@router.message(Command("add_account"))
@router.message(F.text.lower() == "подключить аккаунт")
async def add_account(message: types.Message, state: FSMContext) -> None:
    await state.set_state(AddAccount.waiting_token)
    await message.answer("Пришли мне access-token / строку сессии от P2C.")


@router.message(AddAccount.waiting_token)
async def receive_account_token(message: types.Message, state: FSMContext) -> None:
    sessionmaker = get_sessionmaker()
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        await state.clear()
        return

    token = message.text
    if not token:
        await message.answer("Не вижу токен. Пришли строку сессии целиком.")
        return

    async with sessionmaker() as session:
        user = await _get_or_create_user(session, from_user)
        count = await session.scalar(
            select(func.count(CryptoAccount.id)).where(CryptoAccount.user_id == user.id)
        )
        account_name = f"Аккаунт #{(count or 0) + 1}"
        account = CryptoAccount(
            user=user,
            name=account_name,
            access_token_enc=token,
            notification_chat_id=from_user.id,
        )
        session.add(account)
        await session.commit()

    await state.clear()
    await message.answer(f"Готово, сохранил {account_name}.")


@router.message(Command("my_accounts"))
async def my_accounts(message: types.Message) -> None:
    sessionmaker = get_sessionmaker()
    from_user = message.from_user
    if from_user is None:
        await message.answer("Не могу определить пользователя.")
        return

    async with sessionmaker() as session:
        user = await session.scalar(select(User).where(User.tg_id == from_user.id))
        if user is None:
            await message.answer("Пока нет подключённых аккаунтов.")
            return

        accounts = await session.scalars(
            select(CryptoAccount).where(CryptoAccount.user_id == user.id)
        )
        accounts_list = list(accounts)

    if not accounts_list:
        await message.answer("Пока нет подключённых аккаунтов.")
        return

    lines = [
        f"{idx + 1}. {acc.name or 'Без названия'} (chat_id={acc.notification_chat_id})"
        for idx, acc in enumerate(accounts_list)
    ]
    await message.answer("Твои аккаунты:\n" + "\n".join(lines))
