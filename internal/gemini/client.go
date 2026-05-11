package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/queue"
)

const (
	apiURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"

	promptTemplate = `Você é um especialista em marketing de afiliados. Crie uma mensagem curta e animada para divulgar o produto abaixo em um grupo de WhatsApp.

Regras:
- Linguagem informal e animada, português brasileiro
- Use emojis relevantes
- Destaque o desconto e a urgência se houver desconto
- Máximo 5 linhas no total
- Inclua o link de afiliado no final
- Não invente informações que não foram fornecidas

Dados do produto:
Nome: %s
Preço: R$ %.2f
Desconto: %d%%
Link: %s`
)

type Client struct {
	apiKey     string
	httpClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) GenerateMessage(p queue.Product) (string, error) {
	prompt := fmt.Sprintf(promptTemplate,
		p.Title,
		p.Price,
		p.Discount,
		p.OfferLink,
	)

	body := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("gemini: marshal request: %w", err)
	}

	var lastErr error
	for range 3 {
		msg, err := c.doGenerate(payload)
		if err == nil {
			return msg, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func (c *Client) doGenerate(payload []byte) (string, error) {
	url := apiURL + "?key=" + c.apiKey
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("gemini: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gemini: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gemini: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini: unexpected status %d: %s", resp.StatusCode, body)
	}

	var result geminiResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("gemini: decode response: %w", err)
	}

	if result.Error != nil {
		return "", fmt.Errorf("gemini: api error: %s", result.Error.Message)
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini: resposta vazia")
	}

	return result.Candidates[0].Content.Parts[0].Text, nil
}
