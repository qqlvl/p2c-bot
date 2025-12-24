"""Reply keyboards for the bot."""

from aiogram.types import KeyboardButton, ReplyKeyboardMarkup

BTN_ADD_ACCOUNT = "➕ Подключить аккаунт"
BTN_LIST_ACCOUNTS = "📂 Мои аккаунты"

main_menu_kb = ReplyKeyboardMarkup(
    keyboard=[
        [KeyboardButton(text=BTN_ADD_ACCOUNT)],
        [KeyboardButton(text=BTN_LIST_ACCOUNTS)],
    ],
    resize_keyboard=True,
    is_persistent=True,
)
