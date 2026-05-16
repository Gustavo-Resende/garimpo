package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/Gustavo-Resende/garimpo/internal/queue"
)

const apiBase = "https://api.telegram.org/bot%s/%s"

type Client struct {
	botToken   string
	chatID     string
	httpClient *http.Client
}

func NewClient(botToken, chatID string) *Client {
	return &Client{
		botToken: botToken,
		chatID:   chatID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendProductForReview envia um produto para o grupo de curadoria com botões inline.
// Retorna o message_id da mensagem enviada.
func (c *Client) SendProductForReview(p queue.Product) (int, error) {
	caption := buildCaption(p)

	keyboard := inlineKeyboard{
		InlineKeyboard: [][]inlineButton{{
			{Text: "✅ Aprovar", CallbackData: fmt.Sprintf("approve:%d", p.ID)},
			{Text: "❌ Recusar", CallbackData: fmt.Sprintf("reject:%d", p.ID)},
			{Text: "🖼️ Trocar imagem", CallbackData: fmt.Sprintf("change_image:%d", p.ID)},
		}},
	}

	replyMarkup, err := json.Marshal(keyboard)
	if err != nil {
		return 0, fmt.Errorf("telegram: marshal keyboard: %w", err)
	}

	payload := map[string]any{
		"chat_id":      c.chatID,
		"photo":        p.ImageURL,
		"caption":      caption,
		"parse_mode":   "HTML",
		"reply_markup": string(replyMarkup),
	}

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description"`
	}

	if err := c.post("sendPhoto", payload, &result); err != nil {
		return 0, err
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram: sendPhoto: %s", result.Description)
	}
	return result.Result.MessageID, nil
}

// SendProductForReviewWithFileID é igual a SendProductForReview mas usa file_id
// em vez de URL — necessário para fotos enviadas por usuários no próprio Telegram.
func (c *Client) SendProductForReviewWithFileID(p queue.Product, fileID string) (int, error) {
	caption := buildCaption(p)

	keyboard := inlineKeyboard{
		InlineKeyboard: [][]inlineButton{{
			{Text: "✅ Aprovar", CallbackData: fmt.Sprintf("approve:%d", p.ID)},
			{Text: "❌ Recusar", CallbackData: fmt.Sprintf("reject:%d", p.ID)},
			{Text: "🖼️ Trocar imagem", CallbackData: fmt.Sprintf("change_image:%d", p.ID)},
		}},
	}

	replyMarkup, err := json.Marshal(keyboard)
	if err != nil {
		return 0, fmt.Errorf("telegram: marshal keyboard: %w", err)
	}

	payload := map[string]any{
		"chat_id":      c.chatID,
		"photo":        fileID,
		"caption":      caption,
		"parse_mode":   "HTML",
		"reply_markup": string(replyMarkup),
	}

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description"`
	}

	if err := c.post("sendPhoto", payload, &result); err != nil {
		return 0, err
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram: sendPhoto (file_id): %s", result.Description)
	}
	return result.Result.MessageID, nil
}

// SendProductForReviewUpload baixa a imagem do produto e envia via multipart/form-data.
// Usar quando a URL da imagem não é acessível diretamente pelo Telegram (ex: Mercado Livre).
func (c *Client) SendProductForReviewUpload(p queue.Product) (int, error) {
	raw, err := c.downloadImage(p.ImageURL)
	if err != nil {
		return 0, fmt.Errorf("telegram: download imagem: %w", err)
	}
	imageData := toJPEG(raw)

	caption := buildCaption(p)
	keyboard := inlineKeyboard{
		InlineKeyboard: [][]inlineButton{{
			{Text: "✅ Aprovar", CallbackData: fmt.Sprintf("approve:%d", p.ID)},
			{Text: "❌ Recusar", CallbackData: fmt.Sprintf("reject:%d", p.ID)},
			{Text: "🖼️ Trocar imagem", CallbackData: fmt.Sprintf("change_image:%d", p.ID)},
		}},
	}
	replyMarkup, err := json.Marshal(keyboard)
	if err != nil {
		return 0, fmt.Errorf("telegram: marshal keyboard: %w", err)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	_ = writeFormField(w, "chat_id", c.chatID)
	_ = writeFormField(w, "caption", caption)
	_ = writeFormField(w, "parse_mode", "HTML")
	_ = writeFormField(w, "reply_markup", string(replyMarkup))

	fw, err := w.CreateFormFile("photo", "image.jpg")
	if err != nil {
		return 0, fmt.Errorf("telegram: criar form file: %w", err)
	}
	if _, err := fw.Write(imageData); err != nil {
		return 0, fmt.Errorf("telegram: escrever imagem no form: %w", err)
	}
	w.Close()

	url := fmt.Sprintf(apiBase, c.botToken, "sendPhoto")
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return 0, fmt.Errorf("telegram: build request sendPhoto multipart: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("telegram: http sendPhoto multipart: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("telegram: ler resposta sendPhoto multipart: %w", err)
	}

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("telegram: decode sendPhoto multipart: %w", err)
	}
	if !result.OK {
		return 0, fmt.Errorf("telegram: sendPhoto multipart: %s", result.Description)
	}
	return result.Result.MessageID, nil
}

// toJPEG decodifica a imagem (JPEG, PNG, WebP, etc.) e re-encodifica como JPEG.
// Se não conseguir decodificar, retorna os bytes originais como fallback.
func toJPEG(data []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
		return data
	}
	return out.Bytes()
}

func (c *Client) downloadImage(imageURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d ao baixar imagem", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func writeFormField(w *multipart.Writer, field, value string) error {
	fw, err := w.CreateFormField(field)
	if err != nil {
		return err
	}
	_, err = fw.Write([]byte(value))
	return err
}

// EditMessageCaption edita o caption de uma mensagem existente (sem botões).
func (c *Client) EditMessageCaption(messageID int, newCaption string) error {
	payload := map[string]any{
		"chat_id":    c.chatID,
		"message_id": messageID,
		"caption":    newCaption,
		"parse_mode": "HTML",
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}

	if err := c.post("editMessageCaption", payload, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram: editMessageCaption: %s", result.Description)
	}
	return nil
}

// SendNotification envia uma mensagem de texto simples para o grupo.
func (c *Client) SendNotification(text string) error {
	payload := map[string]any{
		"chat_id": c.chatID,
		"text":    text,
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}

	if err := c.post("sendMessage", payload, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("telegram: sendMessage: %s", result.Description)
	}
	return nil
}

func (c *Client) post(method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal %s: %w", method, err)
	}

	url := fmt.Sprintf(apiBase, c.botToken, method)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build request %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: http %s: %w", method, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("telegram: read body %s: %w", method, err)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("telegram: decode response %s: %w", method, err)
	}
	return nil
}

// buildCaption gera o caption HTML para o produto.
func buildCaption(p queue.Product) string {
	title := buildTitle(p.Title)
	sourceLine := sourceLabel(p.Source)

	if p.Discount > 0 {
		original := p.Price / (1 - float64(p.Discount)/100)
		original = math.Round(original*100) / 100
		return fmt.Sprintf(
			"<b>%s</b>\n\n%s 📦\n\n❌ De <s>R$ %.2f</s>\n✅ por R$ %.2f\n\n👉 %s\n\n%s",
			title, p.Title, original, p.Price, p.OfferLink, sourceLine,
		)
	}
	return fmt.Sprintf(
		"<b>%s</b>\n\n%s 📦\n\nR$ %.2f\n\n👉 %s\n\n%s",
		title, p.Title, p.Price, p.OfferLink, sourceLine,
	)
}

func sourceLabel(source string) string {
	switch source {
	case "mercadolivre":
		return "🛒 Oferta verificada no Mercado Livre"
	default:
		return "🛒 Oferta verificada na Shopee"
	}
}

// buildTitle pega as primeiras 4 palavras do nome e retorna em caixa alta.
func buildTitle(name string) string {
	words := strings.Fields(name)
	if len(words) > 4 {
		words = words[:4]
	}
	return strings.ToUpper(strings.Join(words, " "))
}

type inlineKeyboard struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}
