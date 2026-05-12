package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/queue"
)

const apiURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent"

const promptTemplate = `Você é um especialista em marketing de afiliados. Gere uma caption curta para acompanhar a imagem de um produto no WhatsApp.

Formato obrigatório (use exatamente 4 blocos separados por linha em branco):
[nome do produto simples e direto, com emoji relevante]

[preço e desconto em destaque, ex: "💰 R$ 49,90 — 40%% OFF"]

[uma linha curta e animada sobre o produto, máximo 1 linha]

[link]

Regras:
- O link deve ser a última linha, sozinho, sem nenhum texto antes dele
- Nunca usar frases como "Garanta o seu aqui:", "Acesse:", "Clique aqui:" antes do link
- Tom informal em português brasileiro
- Não inventar informações que não foram passadas
- O texto vai como caption de imagem, deve ser curto e direto

Dados do produto:
Nome: %s
Preço: R$ %.2f
Desconto: %d%%
Link: %s`

const evalPromptTemplate = `Você é um curador de ofertas para um grupo de WhatsApp brasileiro de achadinhos.
Avalie se o produto abaixo é adequado para divulgação.
Produtos ADEQUADOS:
- Itens de casa e cozinha (utensílios, potes, organizadores)
- Eletrodomésticos pequenos (air fryer, micro-ondas, chaleira, panela elétrica)
- Roupas de academia (dry fit, legging, shorts esportivos)
- Moda masculina e feminina em tendência (oversized, cargo, streetwear)
- Beleza e cuidados pessoais (skincare, perfumes, cabelo, barba)
- Esportes e academia (equipamentos, tênis, garrafa d'água, óculos)
- Pets (acessórios, alimentação)
- Saúde (suplementos, equipamentos)
- Mãe e bebê

Produtos INADEQUADOS:
- Games, consoles, controles (PlayStation, Xbox, Nintendo)
- Informática cara (notebooks, monitores, placas de vídeo)
- Grandes eletrodomésticos (geladeira, fogão, máquina de lavar, ar condicionado)
- Peças e acessórios para carros ou motos
- Instrumentos musicais
- Produtos sem apelo popular ou muito nichados

Responda APENAS com JSON válido, sem texto adicional:
{"aprovado": true} ou {"aprovado": false, "motivo": "..."}
Produto: %s
Preço: R$ %.2f`

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
	Contents         []geminiContent   `json:"contents"`
	GenerationConfig *generationConfig `json:"generationConfig,omitempty"`
}

type generationConfig struct {
	ResponseMIMEType string `json:"responseMimeType,omitempty"`
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

type evalResult struct {
	Aprovado bool   `json:"aprovado"`
	Motivo   string `json:"motivo"`
}

// callAPI executa o POST para a API Gemini e retorna o texto da resposta.
func (c *Client) callAPI(body geminiRequest) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("gemini: marshal request: %w", err)
	}

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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gemini: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini: unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
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

func (c *Client) GenerateMessage(p queue.Product) (string, error) {
	prompt := fmt.Sprintf(promptTemplate, p.Title, p.Price, p.Discount, p.OfferLink)
	body := geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
	}

	var lastErr error
	for range 3 {
		msg, err := c.callAPI(body)
		if err == nil {
			return msg, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// EvaluateProduct avalia se um produto é adequado para divulgação.
// Retorna (aprovado, motivo, erro). Em caso de erro, o caller decide se enfileira mesmo assim.
func (c *Client) EvaluateProduct(title string, price float64) (bool, string, error) {
	prompt := fmt.Sprintf(evalPromptTemplate, title, price)
	body := geminiRequest{
		Contents:         []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
		GenerationConfig: &generationConfig{ResponseMIMEType: "application/json"},
	}

	var lastErr error
	for range 3 {
		approved, motivo, err := c.doEvaluate(body)
		if err == nil {
			return approved, motivo, nil
		}
		lastErr = err
	}
	return false, "", lastErr
}

func (c *Client) doEvaluate(body geminiRequest) (bool, string, error) {
	text, err := c.callAPI(body)
	if err != nil {
		return false, "", err
	}

	// Limpa eventual markdown code fence caso o modelo ignore a instrução.
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var result evalResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return false, "", fmt.Errorf("gemini: parse eval response %q: %w", text, err)
	}

	return result.Aprovado, result.Motivo, nil
}
