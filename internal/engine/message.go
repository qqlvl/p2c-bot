package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"p2c-engine/internal/p2c"
)

func formatAmountWei(val string) float64 {
	// convert string representing wei (1e18) to float
	if val == "" {
		return 0
	}
	// best-effort parsing; ignore errors
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0
	}
	return f / 1e18
}

func buildMessage(p p2c.Payment, success bool, errText string) string {
	outAmount := formatAmountWei(p.Amount)
	reward := formatAmountWei(p.RewardAmount)

	var sb strings.Builder
	if success {
		sb.WriteString("🤖 Заявка взята автоматически ✅\n")
	} else {
		sb.WriteString("⚠️ Не удалось взять заявку\n")
	}

	sb.WriteString(fmt.Sprintf("Бренд: %s\n", p.BrandName))
	sb.WriteString(fmt.Sprintf("Сумма: %s %s\n", p.AmountFiat, p.Fiat))
	sb.WriteString(fmt.Sprintf("Получает: %.6f %s\n", outAmount, p.Asset))
	sb.WriteString(fmt.Sprintf("Курс: %s\n", p.ExchangeRate))
	sb.WriteString(fmt.Sprintf("Вознаграждение: %.6f %s\n", reward, p.Asset))
	if p.URL != "" {
		sb.WriteString(fmt.Sprintf("QR: %s\n", p.URL))
	}
	sb.WriteString(fmt.Sprintf("ID: %s\n", p.ID))
	if !success && errText != "" {
		sb.WriteString(fmt.Sprintf("Ошибка: %s\n", errText))
	}
	return sb.String()
}

func sendMessage(botToken string, chatID int64, text string) error {
	body := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	data, _ := json.Marshal(body)
	resp, err := http.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken),
		"application/json",
		bytes.NewReader(data),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status %d", resp.StatusCode)
	}
	return nil
}
