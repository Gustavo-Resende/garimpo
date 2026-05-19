package shopee

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const apiURL = "https://open-api.affiliate.shopee.com.br/graphql"

// validShopTypes são os tipos de loja aceitos: 1=Official, 2=Preferred, 4=Preferred Plus.
var validShopTypes = []int{1, 2, 4}

var itemIDPattern = regexp.MustCompile(`i\.(\d+)\.(\d+)`)

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

// hasValidShopType retorna true se o produto tiver pelo menos um tipo em validShopTypes.
func hasValidShopType(shopType []int) bool {
	for _, t := range shopType {
		if slices.Contains(validShopTypes, t) {
			return true
		}
	}
	return false
}

const maxAPILimit = 50

func (c *Client) FetchPage(cfg FilterConfig, limit, page int) (nodes []ProductNode, hasNextPage bool, err error) {
	if limit > maxAPILimit {
		limit = maxAPILimit
	}
	query := fmt.Sprintf(
		`{"query":"{ productOfferV2(sortType: 2, page: %d, limit: %d) { nodes { itemId productName productLink offerLink imageUrl priceMin priceMax priceDiscountRate sales ratingStar commissionRate commission shopName shopType } pageInfo { page limit hasNextPage } } }"}`,
		page, limit,
	)

	var lastErr error
	for range 3 {
		n, hnp, e := c.doFetch(query, cfg)
		if e == nil {
			return n, hnp, nil
		}
		lastErr = e
	}
	return nil, false, lastErr
}

// doRequest executa uma query GraphQL e retorna a resposta parseada sem aplicar filtros.
func (c *Client) doRequest(query string) (*productOfferResponse, error) {
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

	return &result, nil
}

func (c *Client) doFetch(query string, cfg FilterConfig) ([]ProductNode, bool, error) {
	result, err := c.doRequest(query)
	if err != nil {
		return nil, false, err
	}

	total := len(result.Data.ProductOfferV2.Nodes)
	passedMinCommission, passedSales, passedRating, passedMaxCommission, passedShopType := 0, 0, 0, 0, 0

	var filtered []ProductNode
	for _, n := range result.Data.ProductOfferV2.Nodes {
		p := n.toProductNode()

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

		if !hasValidShopType(p.ShopType) {
			continue
		}
		passedShopType++

		filtered = append(filtered, p)
	}

	slog.Info("shopee: filtros aplicados",
		"total_api", total,
		"passou_min_commission", passedMinCommission,
		"passou_sales", passedSales,
		"passou_rating", passedRating,
		"passou_max_commission", passedMaxCommission,
		"passou_shop_type", passedShopType,
		"final", len(filtered),
	)

	return filtered, result.Data.ProductOfferV2.PageInfo.HasNextPage, nil
}

type shopeePublicResponse struct {
	Data struct {
		Name        string   `json:"name"`
		Price       int64    `json:"price"`
		PriceMax    int64    `json:"price_max"`
		RawDiscount int      `json:"raw_discount"`
		Images      []string `json:"images"`
	} `json:"data"`
	Error    int    `json:"error"`
	ErrorMsg string `json:"error_msg"`
}

func urlUnescape(s string) (string, error) { return url.PathUnescape(s) }

// extractItemIDs extrai shopId e itemId do padrão i.{shopId}.{itemId} presente na URL da Shopee.
func extractItemIDs(rawURL string) (shopID, itemID int64, err error) {
	m := itemIDPattern.FindStringSubmatch(rawURL)
	if m == nil {
		return 0, 0, fmt.Errorf("shopee: padrão i.shopId.itemId não encontrado na URL")
	}
	shopID, _ = strconv.ParseInt(m[1], 10, 64)
	itemID, _ = strconv.ParseInt(m[2], 10, 64)
	return shopID, itemID, nil
}

// FetchByURL busca um produto específico pela URL da Shopee.
// Extrai o itemId da URL e chama productOfferV2(itemId: <int>, limit: 1).
// Se não encontrar dados completos via afiliado, tenta a API pública da Shopee.
func (c *Client) FetchByURL(rawURL string) (*ProductNode, error) {
	// Remove query string antes de aplicar o regex para evitar falsos positivos.
	cleanURL := rawURL
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		cleanURL = rawURL[:idx]
	}
	// Faz unescape de %XX antes do regex (ex: URLs copiadas do browser).
	if unescaped, err := urlUnescape(cleanURL); err == nil {
		cleanURL = unescaped
	}

	shopID, itemID, extractErr := extractItemIDs(cleanURL)
	if extractErr != nil {
		return nil, fmt.Errorf("shopee: não foi possível extrair itemId da URL: %w", extractErr)
	}

	slog.Info("shopee: FetchByURL", "shopId", shopID, "itemId", itemID)

	query := fmt.Sprintf(
		`{"query":"{ productOfferV2(itemId: %d, limit: 1) { nodes { itemId productName productLink offerLink imageUrl priceMin priceMax priceDiscountRate sales ratingStar commissionRate commission shopName shopType } pageInfo { page limit hasNextPage } } }"}`,
		itemID,
	)
	if result, err := c.doRequest(query); err == nil && len(result.Data.ProductOfferV2.Nodes) > 0 {
		n := result.Data.ProductOfferV2.Nodes[0].toProductNode()
		if n.ProductName != "" && n.PriceMax > 0 {
			return &n, nil
		}
	} else if err != nil {
		slog.Warn("shopee: FetchByURL productOfferV2 falhou", "err", err)
	}

	// Fallback: API pública da Shopee para nome/preço/imagem (sem comissão).
	publicNode, err := c.fetchPublicData(shopID, itemID)
	if err != nil || publicNode.ProductName == "" || publicNode.PriceMax == 0 || publicNode.ImageURL == "" {
		if err != nil {
			slog.Warn("shopee: fetchPublicData falhou", "err", err)
		}
		return nil, fmt.Errorf("shopee: não foi possível extrair dados do produto")
	}
	publicNode.OfferLink = rawURL
	return publicNode, nil
}

// fetchPublicData busca nome, preço e imagem via API pública da Shopee (sem autenticação).
// Não retorna dados de afiliado como comissão.
func (c *Client) fetchPublicData(shopID, itemID int64) (*ProductNode, error) {
	url := fmt.Sprintf("https://shopee.com.br/api/v4/item/get?itemid=%d&shopid=%d", itemID, shopID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("shopee: public api build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://shopee.com.br/")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shopee: public api http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("shopee: public api read: %w", err)
	}

	var result shopeePublicResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("shopee: public api decode: %w", err)
	}
	if result.Error != 0 {
		return nil, fmt.Errorf("shopee: public api error %d: %s", result.Error, result.ErrorMsg)
	}

	imageURL := ""
	if len(result.Data.Images) > 0 {
		imageURL = fmt.Sprintf("https://cf.shopee.com.br/file/%s", result.Data.Images[0])
	}

	priceMax := float64(result.Data.PriceMax) / 100000
	if priceMax == 0 {
		priceMax = float64(result.Data.Price) / 100000
	}

	return &ProductNode{
		ItemID:            itemID,
		ProductName:       result.Data.Name,
		PriceMax:          priceMax,
		PriceDiscountRate: result.Data.RawDiscount,
		ImageURL:          imageURL,
	}, nil
}

