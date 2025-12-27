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
	idStr := p.IDString()

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
	sb.WriteString(fmt.Sprintf("ID: %s\n", idStr))
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

// sendPhoto sends a photo by URL with caption and optional reply_markup.
func sendPhoto(botToken string, chatID int64, photoURL, caption string, markup map[string]any) error {
	body := map[string]any{
		"chat_id": chatID,
		"photo":   photoURL,
	}
	if caption != "" {
		body["caption"] = caption
		body["parse_mode"] = "HTML"
	}
	if markup != nil {
		body["reply_markup"] = markup
	}
	data, _ := json.Marshal(body)
	resp, err := http.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", botToken),
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

// buildLiveCaption formats live payment info with status text.
func buildLiveCaption(p p2c.LivePayment, status string) string {
	var sb strings.Builder
	if status != "" {
		sb.WriteString(status + "\n")
	}
	sb.WriteString(fmt.Sprintf("ID: %s\n", p.ID))
	reward := formatAmountWei(p.FeeAmount)
	outAsset := p.OutAsset
	if outAsset == "" {
		outAsset = "USDT"
	}

	sb.WriteString(fmt.Sprintf("Бренд: %s\n", p.BrandName))
	sb.WriteString(fmt.Sprintf("Сумма: %s %s\n", p.InAmount, p.InAsset))
	sb.WriteString(fmt.Sprintf("Курс: %s\n", p.ExchangeRate))
	sb.WriteString(fmt.Sprintf("Вознаграждение: %.4f %s\n", reward, outAsset))
	return sb.String()
}

// buildPaidKeyboard builds inline keyboard with callback payload carrying account/payment and amounts.
func buildPaidKeyboard(accID int64, p p2c.LivePayment) map[string]any {
	if p.ID == "" || accID == 0 {
		return nil
	}
	// payload: paid:<acc>:<payID>:<amount>:<rate>:<fee>
	paidPayload := fmt.Sprintf(
		"paid:%d:%s:%s:%s:%s",
		accID, p.ID, p.InAmount, p.ExchangeRate, p.FeeAmount,
	)
	cancelPayload := fmt.Sprintf("cancel:%d:%s", accID, p.ID)
	return map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{
					"text":         "✅ Я оплатил",
					"callback_data": paidPayload,
				},
				{
					"text":         "❌ Отменить",
					"callback_data": cancelPayload,
				},
			},
		},
	}
}
