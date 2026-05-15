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

❌ De ~R$ [preço original]~
✅ por R$ [preço atual] 🦸🏻‍♂️

👉 [link]

Exemplos:

MIZUNO DE CORRIDA
Tênis Mizuno Goya 👟
❌ De ~R$ 349~
✅ por R$ 188 🦸🏻‍♂️
👉 https://s.shopee.com.br/xxx

7 CONTO CADA POTE
Kit 10 Potes de Vidro Hermético 🫙
❌ De ~R$ 149~
✅ por R$ 73 🦸🏻‍♂️
👉 https://s.shopee.com.br/xxx

KIT COMPLETO PRA TREINO
Kit 5 Shorts Masculino Dry Fit 🩳
❌ De ~R$ 180~
✅ por R$ 84 🦸🏻‍♂️
👉 https://s.shopee.com.br/xxx

Como calcular o preço original:
precoOriginal = priceMax / (1 - priceDiscountRate/100)
Arredonde para 2 casas decimais.
Se priceDiscountRate for 0, omita as linhas De/por e mostre apenas: R$ [priceMax]
Regras obrigatórias:
1. TÍTULO em caixa alta, criativo, máx 4 palavras
2. Nome do produto limpo, sem repetição, sem caixa alta desnecessária
3. Emoji temático no final do nome
4. Linha de preço no formato exato: ❌ De ~R$ [original]~ / ✅ por R$ [atual] 🦸🏻‍♂️
5. Link sempre precedido de 👉, última linha, nada depois
6. \n\n entre cada bloco
7. Tom informal em português brasileiro
8. Não inventar preços

Dados do produto:
Nome: %s
Preço atual (priceMax): R$ %.2f
Desconto (priceDiscountRate): %d%%
Link: %s`

const evalPromptTemplate = `Você é um curador de ofertas para um canal de achadinhos no WhatsApp brasileiro. Público misto, 25 anos pra cima. Clima atual: VERÃO/CALOR — não aprovar produtos de frio.

CATEGORIAS PERMITIDAS E COTAS (para uma fila de 30 produtos):

1. Tênis (casual, corrida, streetwear, futebol) → máximo 3
2. Kit meias → máximo 2
3. Kit cuecas / kit calcinha → máximo 2
4. Bolsa e mochila (masculina e feminina) → máximo 2
5. Academia: creatina, whey, pré-treino, hipercalórico, legging, dry fit, shorts fitness → máximo 4
6. Relógios → máximo 2
7. Utensílios de cozinha: kit facas, talheres, canecas, copos, jarras, cafeteiras, tupperware, marmitas, assadeiras → máximo 4
8. Eletrodomésticos pequenos: air fryer, micro-ondas, kit panela → máximo 3
9. Cuidado pessoal: perfume, kit shampoo, condicionador → máximo 3
10. Banheiro: toalha, toalha de rosto, sabonete líquido, cesta de roupa suja, organizador, cheirinho, espelho → máximo 3
11. Quarto: kit cabides, organizador de maquiagem, espelho, penteadeira, luminária, highlight, kit lençol, edredom, travesseiro, roupa de cama → máximo 3
12. Decoração de sala: puff, quadros, ambilight, itens decorativos → máximo 2

TOTAL ALVO: 30 produtos bem distribuídos entre todas as categorias acima.

PRODUTOS PROIBIDOS (rejeitar sempre):
- Moletons, jaquetas, casacos, agasalhos, roupas de frio
- Games, consoles, controles
- Notebooks, monitores, placas de vídeo
- Grandes eletrodomésticos (geladeira, fogão, máquina de lavar)
- Peças automotivas
- Livros, materiais didáticos, itens religiosos
- Smartwatch genérico, AirPods genéricos, fones TWS sem marca
- Projetores, mini consoles
- Kits de roupas genéricos sem marca identificável
- Tênis ou calçado sem marca reconhecida
- Produtos com títulos suspeitos: "4K Ultra HD Original" em itens genéricos

PRIORIZAÇÃO POR MARCA (vestuário e calçados):
Marcas prioritárias com desconto >= 15%%:
- Esportes: Nike, Adidas, Puma, Mizuno, Olympikus, Under Armour, Asics, New Balance, Fila
- Moda: Aramis, Reserva, Hering, Alpha Co, Lacoste, Tommy Hilfiger, Polo Wear, Insider
- Calçados: Vans, Converse, Skechers, Havaianas, Rider
- Suplementos: Black Skull, FTW, Growth, Max Titanium, Integral Médica, Probiótica

Regra de desconto para marcas:
- Marca reconhecida + desconto >= 15%% → prioridade MÁXIMA
- Marca reconhecida + desconto < 15%% → prioridade NORMAL
- Sem marca reconhecida em vestuário/calçado → reprovar

Exceções sem necessidade de marca:
- Roupas íntimas (cuecas, meias, calcinha)
- Acessórios de academia (legging, top, shorts fitness)
- Utensílios de cozinha, banheiro, quarto e decoração

Responda APENAS com JSON válido:
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
			return msg + "\n\n" + sourceLabel(p.Source), nil
		}
		lastErr = err
	}
	return "", lastErr
}

func sourceLabel(source string) string {
	if source == "mercadolivre" {
		return "🛒 Oferta verificada no Mercado Livre"
	}
	return "🛒 Oferta verificada na Shopee"
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
