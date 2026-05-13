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

const promptTemplate = `Gere uma mensagem de divulgação para WhatsApp seguindo EXATAMENTE este formato:

[TÍTULO CHAMATIVO EM CAIXA ALTA — máx 4 palavras, criativo e direto]

[Nome do produto limpo e curto] [emoji temático]

De R$ [preço original] por R$ [preço atual] 🦸🏻‍♂️

👉 [link]

Exemplos:

MIZUNO DE CORRIDA
Tênis Mizuno Goya 👟
De R$ 349 por R$ 188 🦸🏻‍♂️
👉 https://s.shopee.com.br/xxx

7 CONTO CADA POTE
Kit 10 Potes de Vidro Hermético 🫙
De R$ 149 por R$ 73 🦸🏻‍♂️
👉 https://s.shopee.com.br/xxx

KIT COMPLETO PRA TREINO
Kit 5 Shorts Masculino Dry Fit 🩳
De R$ 180 por R$ 84 🦸🏻‍♂️
👉 https://s.shopee.com.br/xxx

Como calcular o preço original:
precoOriginal = priceMax / (1 - priceDiscountRate/100)
Arredonde para 2 casas decimais.
Se priceDiscountRate for 0, omita o De/por e mostre apenas: R$ [priceMax] 🦸🏻‍♂️

Regras obrigatórias:
1. TÍTULO em caixa alta, criativo, máx 4 palavras
2. Nome do produto limpo, sem repetição, sem caixa alta desnecessária
3. Emoji temático no final do nome
4. Linha de preço com 🦸🏻‍♂️ no final
5. Link sempre precedido de 👉, última linha, nada depois
6. \n\n entre cada bloco
7. Tom informal em português brasileiro
8. Não inventar preços

Dados do produto:
Nome: %s
Preço atual (priceMax): R$ %.2f
Desconto (priceDiscountRate): %d%%
Link: %s`

const evalPromptTemplate = `Você é um curador de ofertas para um canal de achadinhos no WhatsApp brasileiro. Público misto, 25 anos pra cima. Seu objetivo é aprovar apenas produtos com alto potencial de conversão.

REGRAS DE DIVERSIFICAÇÃO (por rodada de 20 produtos):
- Moda básica e kits (cuecas, meias, camisetas, moletons, jaquetas, bonés): máximo 5
- Fitness e suplementos (creatina, whey, pré-treino, legging, shorts, top): máximo 4
- Casa e organização (potes, kits cozinha, tapetes, toalhas, organizadores): máximo 4
- Calçados (tênis, chinelos, sandálias): máximo 2
- Beleza e perfumaria: máximo 2
- Cama e banho (cobertores, lençóis, toalhas): máximo 2
- Ferramentas (kit chaves, furadeira, parafusadeira): máximo 1
- Pequenos eletrodomésticos (chaleira, sanduicheira, mixer): máximo 1
- Eletrônicos utilitários (power bank, fone): máximo 1

PRIORIZAÇÃO POR MARCA (em roupas, calçados e acessórios):

Produtos de marcas reconhecidas devem receber prioridade MÁXIMA quando apresentarem desconto real e relevante (priceDiscountRate >= 15%%). Marcas prioritárias:

Esportes e fitness: Nike, Adidas, Puma, Mizuno, Olympikus, Under Armour, Asics, New Balance, Fila, Penalty, Umbro
Moda masculina: Aramis, Reserva, Hering, Alpha Co, Lacoste, Tommy Hilfiger, Polo Wear, Insider, Polo Ralph Lauren
Moda feminina: Forum, Animale, Colcci, Shoulder, Farm
Calçados: Vans, Converse, Skechers, Havaianas, Rider
Suplementos: Black Skull, FTW, Growth, Max Titanium, Integral Médica, Probiótica

REGRA DE DESCONTO PARA MARCAS:
- Marca reconhecida + desconto >= 15%% → prioridade MÁXIMA
- Marca reconhecida + desconto < 15%% → aprovado com prioridade NORMAL
- Sem marca reconhecida → seguir regras abaixo

Exemplos:
- Polo Aramis de R$ 279 por R$ 125 (55%% OFF) → prioridade máxima
- Tênis Nike com 5%% de desconto → aprovado com prioridade normal
- Kit Hering com 40%% OFF → prioridade máxima

Para produtos de vestuário e calçados SEM marca reconhecida no título:
- Se o título contiver palavras como "atacado", "bloguerinha", "modinha", "genérico" → rejeitar
- Se for kit de roupas sem nenhuma marca identificável e com título genérico (ex: "Kit 5 Camisetas Masculinas Básicas") → reprovar com motivo "kit genérico sem marca"
- Se for tênis ou calçado sem marca reconhecida → reprovar com motivo "calçado sem marca"
- Roupas íntimas (cuecas, meias, sutiã) e acessórios de academia (legging, top, shorts) podem ser aprovados sem marca reconhecida pois o apelo é pelo produto em si

PRODUTOS DE POTENCIAL MUITO ALTO (priorizar):
- Kit cuecas, kit meias, kit camisetas dry fit, moletons, jaquetas, casacos
- Creatina, whey protein, pré-treino, termogênico
- Legging, shorts academia, top esportivo, camiseta dry fit
- Tênis de corrida, streetwear, futebol, casual (marcas: Puma, Olympikus, Nike, Adidas)
- Potes herméticos, marmitas, organizadores de cozinha
- Toalhas, tapetes, organizadores de casa
- Perfumes e colônias (masculino e feminino)
- Bolsas e mochilas
- Cobertores, jogos de cama (especialmente no frio: maio-agosto)

PRODUTOS DE POTENCIAL MÉDIO (aprovar com moderação):
- Furadeiras, parafusadeiras, kit ferramentas (máx 1 por rodada)
- Chaleira elétrica, sanduicheira, mixer
- Bolsas femininas, óculos de sol
- Skincare e cosméticos femininos

PRODUTOS PARA REJEITAR:
- Smartwatch genérico, AirPods genéricos, fones TWS sem marca
- Projetores, mini consoles, entretenimento eletrônico
- Notebooks, monitores, placas de vídeo
- Grandes eletrodomésticos (geladeira, fogão, máquina de lavar)
- Peças automotivas
- Livros, materiais didáticos, itens religiosos
- Produtos com títulos suspeitos: "4K Ultra HD Original" em itens genéricos
- Mais de 1 produto quase idêntico na mesma rodada (ex: 3 kits de chave catraca)

SAZONALIDADE ATUAL (maio-agosto = inverno):
- Priorizar: moletons, casacos, jaquetas, cobertores, meias térmicas

Responda APENAS com JSON válido, sem texto adicional:
{"aprovado": true} ou {"aprovado": false, "motivo": "..."}

Produto: %s
Preço: R$ %.2f
Desconto: %d%%`

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
func (c *Client) EvaluateProduct(title string, price float64, discount int) (bool, string, error) {
	prompt := fmt.Sprintf(evalPromptTemplate, title, price, discount)
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
