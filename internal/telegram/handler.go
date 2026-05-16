package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/queue"
)

type Handler struct {
	client         *Client
	q              *queue.Queue
	log            *slog.Logger
	onExtract      func() int
	n8nWebhookURL  string
	mu             sync.Mutex
	awaitingImage  map[int]int // messageID → productID
}

func NewHandler(client *Client, q *queue.Queue, log *slog.Logger, onExtract func() int, n8nWebhookURL string) *Handler {
	return &Handler{
		client:        client,
		q:             q,
		log:           log,
		onExtract:     onExtract,
		n8nWebhookURL: n8nWebhookURL,
		awaitingImage: make(map[int]int),
	}
}

// Run inicia o loop de long polling. Bloqueia até ctx ser cancelado.
func (h *Handler) Run(ctx context.Context) {
	var offset int
	h.log.Info("telegram: handler iniciado")

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := h.getUpdates(offset)
		if err != nil {
			h.log.Error("telegram: getUpdates", "err", err)
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1
			h.processUpdate(u)
		}
	}
}

func (h *Handler) processUpdate(u update) {
	switch {
	case u.CallbackQuery != nil:
		h.handleCallback(u.CallbackQuery)
	case u.Message != nil && len(u.Message.Photo) > 0:
		h.handlePhoto(u.Message)
	case u.Message != nil && u.Message.Text != "":
		h.handleText(u.Message)
	}
}

func (h *Handler) handleText(msg *message) {
	switch {
	case msg.Text == "/buscar":
		if err := h.client.SendNotification("🔍 Buscando novos produtos..."); err != nil {
			h.log.Warn("telegram: SendNotification /buscar início", "err", err)
		}
		added := h.onExtract()
		reply := fmt.Sprintf("✅ Busca concluída — %d novos produtos enviados para curadoria", added)
		if err := h.client.SendNotification(reply); err != nil {
			h.log.Warn("telegram: SendNotification /buscar resultado", "err", err)
		}

	case strings.HasPrefix(msg.Text, "/ml "):
		link := strings.TrimPrefix(msg.Text, "/ml ")
		if !strings.HasPrefix(link, "http") {
			if err := h.client.SendNotification("❌ Link inválido. Use: /ml https://..."); err != nil {
				h.log.Warn("telegram: SendNotification /ml link inválido", "err", err)
			}
			return
		}
		h.handleMLLink(link)
	}
}

func (h *Handler) handleMLLink(link string) {
	if h.n8nWebhookURL == "" {
		h.log.Warn("telegram: /ml recebido mas N8N_WEBHOOK_URL não configurado")
		if err := h.client.SendNotification("❌ Webhook n8n não configurado."); err != nil {
			h.log.Warn("telegram: SendNotification /ml sem webhook", "err", err)
		}
		return
	}

	body, err := json.Marshal(map[string]string{"url": link})
	if err != nil {
		h.log.Error("telegram: /ml json.Marshal", "err", err)
		return
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Post(h.n8nWebhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		h.log.Error("telegram: /ml POST webhook", "err", err)
		if notifyErr := h.client.SendNotification("❌ Erro ao processar o link. Tente novamente."); notifyErr != nil {
			h.log.Warn("telegram: SendNotification /ml erro", "err", notifyErr)
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.log.Error("telegram: /ml webhook retornou status inesperado", "status", resp.StatusCode)
		if notifyErr := h.client.SendNotification("❌ Erro ao processar o link. Tente novamente."); notifyErr != nil {
			h.log.Warn("telegram: SendNotification /ml status erro", "err", notifyErr)
		}
		return
	}

	if err := h.client.SendNotification("✅ Link enviado para processamento! Aguarde alguns instantes e verifique a planilha."); err != nil {
		h.log.Warn("telegram: SendNotification /ml sucesso", "err", err)
	}
	h.log.Info("telegram: /ml link enviado ao n8n", "url", link)
}

func (h *Handler) handleCallback(cb *callbackQuery) {
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		h.log.Warn("telegram: callback_data inválido", "data", cb.Data)
		h.answerCallback(cb.ID, "")
		return
	}
	action, rawID := parts[0], parts[1]
	productID, err := strconv.Atoi(rawID)
	if err != nil {
		h.log.Warn("telegram: id inválido no callback", "data", cb.Data)
		h.answerCallback(cb.ID, "")
		return
	}

	p, err := h.q.GetByID(productID)
	if err != nil || p == nil {
		h.log.Error("telegram: GetByID", "id", productID, "err", err)
		h.answerCallback(cb.ID, "Produto não encontrado")
		return
	}

	switch action {
	case "approve":
		if err := h.q.MarkPending(productID); err != nil {
			h.log.Error("telegram: MarkPending", "id", productID, "err", err)
			h.answerCallback(cb.ID, "Erro ao aprovar")
			return
		}
		newCaption := buildCaption(*p) + "\n\n✅ <b>Aprovado</b>"
		if err := h.client.EditMessageCaption(cb.Message.MessageID, newCaption); err != nil {
			h.log.Warn("telegram: EditMessageCaption após approve", "err", err)
		}
		h.answerCallback(cb.ID, "✅ Aprovado!")
		h.log.Info("telegram: produto aprovado", "id", productID, "title", p.Title)

	case "reject":
		if err := h.q.MarkRejected(productID); err != nil {
			h.log.Error("telegram: MarkRejected", "id", productID, "err", err)
			h.answerCallback(cb.ID, "Erro ao recusar")
			return
		}
		newCaption := buildCaption(*p) + "\n\n❌ <b>Recusado</b>"
		if err := h.client.EditMessageCaption(cb.Message.MessageID, newCaption); err != nil {
			h.log.Warn("telegram: EditMessageCaption após reject", "err", err)
		}
		h.answerCallback(cb.ID, "❌ Recusado")
		h.log.Info("telegram: produto recusado", "id", productID, "title", p.Title)

	case "change_image":
		h.mu.Lock()
		h.awaitingImage[cb.Message.MessageID] = productID
		h.mu.Unlock()
		if err := h.client.SendNotification("📸 Envie a nova imagem para este produto"); err != nil {
			h.log.Warn("telegram: SendNotification change_image", "err", err)
		}
		h.answerCallback(cb.ID, "Aguardando imagem...")

	default:
		h.log.Warn("telegram: ação desconhecida", "action", action)
		h.answerCallback(cb.ID, "")
	}
}

func (h *Handler) handlePhoto(msg *message) {
	h.mu.Lock()
	productID, waiting := h.awaitingImage[msg.ReplyToMessage.MessageID]
	if waiting {
		delete(h.awaitingImage, msg.ReplyToMessage.MessageID)
	}
	h.mu.Unlock()

	if !waiting {
		return
	}

	largest := msg.Photo[len(msg.Photo)-1]

	fileURL, err := h.client.getFileURL(largest.FileID)
	if err != nil {
		h.log.Error("telegram: getFileURL", "file_id", largest.FileID, "err", err)
		return
	}
	if err := h.q.SetImageURL(productID, fileURL); err != nil {
		h.log.Error("telegram: SetImageURL", "id", productID, "err", err)
		return
	}

	p, err := h.q.GetByID(productID)
	if err != nil || p == nil {
		h.log.Error("telegram: GetByID após troca de imagem", "id", productID, "err", err)
		return
	}

	// Reenvia usando file_id — URLs públicas de arquivos de usuários não são
	// acessíveis por bots via sendPhoto com URL.
	newMsgID, err := h.client.SendProductForReviewWithFileID(*p, largest.FileID)
	if err != nil {
		h.log.Error("telegram: SendProductForReviewWithFileID após troca de imagem", "err", err)
		return
	}
	if err := h.q.SetTelegramMessageID(productID, newMsgID); err != nil {
		h.log.Warn("telegram: SetTelegramMessageID após troca de imagem", "err", err)
	}
	h.log.Info("telegram: imagem trocada", "id", productID, "title", p.Title)
}

func (h *Handler) answerCallback(callbackID, text string) {
	payload := map[string]any{
		"callback_query_id": callbackID,
	}
	if text != "" {
		payload["text"] = text
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := h.client.post("answerCallbackQuery", payload, &result); err != nil {
		h.log.Warn("telegram: answerCallbackQuery", "err", err)
	}
}

func (h *Handler) getUpdates(offset int) ([]update, error) {
	payload := map[string]any{
		"timeout":         30,
		"offset":          offset,
		"allowed_updates": []string{"message", "callback_query"},
	}
	var result struct {
		OK          bool     `json:"ok"`
		Result      []update `json:"result"`
		Description string   `json:"description"`
	}
	if err := h.client.post("getUpdates", payload, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("getUpdates: %s", result.Description)
	}
	return result.Result, nil
}

// getFileURL resolve o file_id para uma URL pública de download.
func (c *Client) getFileURL(fileID string) (string, error) {
	payload := map[string]any{"file_id": fileID}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := c.post("getFile", payload, &result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", fmt.Errorf("telegram: getFile: %s", result.Description)
	}
	return fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", c.botToken, result.Result.FilePath), nil
}

// --- tipos para deserializar updates ---

type update struct {
	UpdateID      int            `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID      int         `json:"message_id"`
	Text           string      `json:"text"`
	Photo          []photoSize `json:"photo"`
	ReplyToMessage *message    `json:"reply_to_message"`
}

type photoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *message `json:"message"`
}

