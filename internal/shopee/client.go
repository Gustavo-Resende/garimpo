package shopee

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const apiURL = "https://open-api.affiliate.shopee.com.br/graphql"

// allowedCategories lista os catIds permitidos para filtragem.
// Só é usado se o campo catId estiver disponível na API.
var allowedCategories = []int{
	11059983, // Casa e Construção
	11059984, // Eletrodomésticos
	11059998, // Roupas Femininas
	11059986, // Roupas Masculinas
	11059992, // Esportes e Lazer
	11059974, // Beleza
	11059981, // Saúde
	11059989, // Mãe e Bebê
	11059991, // Animais Domésticos
}

type FilterConfig struct {
	MinCommission float64
	MaxCommission float64
	MinSales      int
	MinRating     float64
}

type Client struct {
	appID      string
	secret     string
	httpClient *http.Client
}

func NewClient(appID, secret string) *Client {
	return &Client{
		appID:  appID,
		secret: secret,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) sign(payload string) (timestamp, signature string) {
	timestamp = strconv.FormatInt(time.Now().Unix(), 10)
	raw := c.appID + timestamp + payload + c.secret
	sum := sha256.Sum256([]byte(raw))
	signature = fmt.Sprintf("%x", sum)
	return
}

func (c *Client) buildAuthHeader(payload string) string {
	ts, sig := c.sign(payload)
	return fmt.Sprintf("SHA256 Credential=%s, Timestamp=%s, Signature=%s", c.appID, ts, sig)
}

type ProductNode struct {
	ItemID            int64
	ProductName       string
	ProductLink       string
	OfferLink         string
	ImageURL          string
	PriceMin          float64
	PriceMax          float64
	PriceDiscountRate int
	Sales             int
	RatingStar        float64
	CommissionRate    float64
	Commission        float64
	ShopName          string
	CatId             int
	ShopType          []int
}

// productNodeRaw mapeia exatamente o JSON da API (itemId é number, demais monetários são strings)
type productNodeRaw struct {
	ItemID            int64  `json:"itemId"`
	ProductName       string `json:"productName"`
	ProductLink       string `json:"productLink"`
	OfferLink         string `json:"offerLink"`
	ImageURL          string `json:"imageUrl"`
	PriceMin          string `json:"priceMin"`
	PriceMax          string `json:"priceMax"`
	PriceDiscountRate int    `json:"priceDiscountRate"`
	Sales             int    `json:"sales"`
	RatingStar        string `json:"ratingStar"`
	CommissionRate    string `json:"commissionRate"`
	Commission        string `json:"commission"`
	ShopName          string `json:"shopName"`
	CatId             int    `json:"catId"`
	ShopType          []int  `json:"shopType"`
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func (r productNodeRaw) toProductNode() ProductNode {
	return ProductNode{
		ItemID:            r.ItemID,
		ProductName:       r.ProductName,
		ProductLink:       r.ProductLink,
		OfferLink:         r.OfferLink,
		ImageURL:          r.ImageURL,
		PriceMin:          parseFloat(r.PriceMin),
		PriceMax:          parseFloat(r.PriceMax),
		PriceDiscountRate: r.PriceDiscountRate,
		Sales:             r.Sales,
		RatingStar:        parseFloat(r.RatingStar),
		CommissionRate:    parseFloat(r.CommissionRate),
		Commission:        parseFloat(r.Commission),
		ShopName:          r.ShopName,
		CatId:             r.CatId,
		ShopType:          r.ShopType,
	}
}

type productOfferResponse struct {
	Data struct {
		ProductOfferV2 struct {
			Nodes    []productNodeRaw `json:"nodes"`
			PageInfo struct {
				Page        int  `json:"page"`
				Limit       int  `json:"limit"`
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
		} `json:"productOfferV2"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func buildQuery(limit int, withCatId, withShopType bool) string {
	extra := ""
	if withCatId {
		extra += " catId"
	}
	if withShopType {
		extra += " shopType"
	}
	return fmt.Sprintf(
		`{"query":"{ productOfferV2(sortType: 2, page: 1, limit: %d) { nodes { itemId productName productLink offerLink imageUrl priceMin priceMax priceDiscountRate sales ratingStar commissionRate commission shopName%s } pageInfo { page limit hasNextPage } } }"}`,
		limit, extra,
	)
}

// isSchemaError verifica se o erro da API é sobre um campo inexistente no schema.
func isSchemaError(err error, field string) bool {
	return err != nil && strings.Contains(err.Error(), `"`+field+`"`)
}

func (c *Client) FetchProducts(cfg FilterConfig, limit int) ([]ProductNode, error) {
	// Tenta primeiro com catId + shopType para investigar ambos os campos.
	query := buildQuery(limit, true, true)
	nodes, catIdOk, err := c.fetchWithFallback(query, cfg, limit)
	if err != nil {
		return nil, err
	}
	if !catIdOk {
		slog.Warn("shopee: campo catId não existe no schema, filtro de categoria desativado")
	}
	return nodes, nil
}

// fetchWithFallback tenta buscar com catId; se a API rejeitar o campo, refaz sem ele.
// Retorna os produtos, se catId estava disponível, e o erro.
func (c *Client) fetchWithFallback(query string, cfg FilterConfig, limit int) ([]ProductNode, bool, error) {
	nodes, err := c.doFetch(query, cfg, true)
	if err == nil {
		return nodes, true, nil
	}

	if isSchemaError(err, "catId") {
		// catId não existe: refaz sem ele mas mantém shopType para logar valores.
		fallback := buildQuery(limit, false, true)
		nodes, err = c.doFetch(fallback, cfg, false)
		if err != nil {
			return nil, false, err
		}
		return nodes, false, nil
	}

	return nil, false, err
}

func (c *Client) doFetch(query string, cfg FilterConfig, applyCatFilter bool) ([]ProductNode, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBufferString(query))
	if err != nil {
		return nil, fmt.Errorf("shopee: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.buildAuthHeader(query))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shopee: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("shopee: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shopee: unexpected status %d: %s", resp.StatusCode, body)
	}

	var result productOfferResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("shopee: decode response: %w", err)
	}

	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("shopee: api error: %s", result.Errors[0].Message)
	}

	total := len(result.Data.ProductOfferV2.Nodes)
	passedMinCommission, passedSales, passedRating, passedMaxCommission, passedCategory := 0, 0, 0, 0, 0

	var filtered []ProductNode
	for _, n := range result.Data.ProductOfferV2.Nodes {
		p := n.toProductNode()

		// Loga shopType de cada produto para investigação do range de valores.
		slog.Debug("shopee: produto", "name", p.ProductName, "catId", p.CatId, "shopType", p.ShopType)

		if p.CommissionRate < cfg.MinCommission {
			continue
		}
		passedMinCommission++

		if p.Sales < cfg.MinSales {
			continue
		}
		passedSales++

		if p.RatingStar < cfg.MinRating {
			continue
		}
		passedRating++

		if p.CommissionRate > cfg.MaxCommission {
			continue
		}
		passedMaxCommission++

		if applyCatFilter && !slices.Contains(allowedCategories, p.CatId) {
			continue
		}
		passedCategory++

		filtered = append(filtered, p)
	}

	slog.Info("shopee: filtros aplicados",
		"total_api", total,
		"passou_min_commission", passedMinCommission,
		"passou_sales", passedSales,
		"passou_rating", passedRating,
		"passou_max_commission", passedMaxCommission,
		"passou_categoria", passedCategory,
		"final", len(filtered),
	)

	return filtered, nil
}
