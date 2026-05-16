package sheets

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	gsheets "google.golang.org/api/sheets/v4"
)

const (
	sheetName = "Página1"
	dataRange = sheetName + "!A2:H"
)

// MLProduct representa um produto do Mercado Livre lido da planilha.
// Colunas: product_name | price | discount | category | image_url | product_link | shop_name | offer_link
type MLProduct struct {
	ProductName string
	Price       float64
	Discount    int
	Category    string
	ImageURL    string
	ProductLink string
	ShopName    string
	OfferLink   string
}

type Client struct {
	svc           *gsheets.Service
	spreadsheetID string
}

func NewClient(credentialsJSON, spreadsheetID string) (*Client, error) {
	ctx := context.Background()
	creds, err := google.CredentialsFromJSON(
		ctx,
		[]byte(credentialsJSON),
		gsheets.SpreadsheetsScope,
	)
	if err != nil {
		return nil, fmt.Errorf("sheets: credenciais inválidas: %w", err)
	}
	svc, err := gsheets.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("sheets: criar serviço: %w", err)
	}
	return &Client{svc: svc, spreadsheetID: spreadsheetID}, nil
}

// ReadAllProducts lê todos os produtos da planilha.
func (c *Client) ReadAllProducts() ([]MLProduct, error) {
	resp, err := c.svc.Spreadsheets.Values.Get(c.spreadsheetID, dataRange).Do()
	if err != nil {
		return nil, fmt.Errorf("sheets: ReadAllProducts: %w", err)
	}
	return parseRows(resp.Values), nil
}

// ReadProductsWithLink retorna apenas os produtos que têm offer_link preenchido.
func (c *Client) ReadProductsWithLink() ([]MLProduct, error) {
	all, err := c.ReadAllProducts()
	if err != nil {
		return nil, err
	}
	var out []MLProduct
	for _, p := range all {
		if strings.TrimSpace(p.OfferLink) != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// ReadProductsWithoutLink retorna apenas os produtos que não têm offer_link.
func (c *Client) ReadProductsWithoutLink() ([]MLProduct, error) {
	all, err := c.ReadAllProducts()
	if err != nil {
		return nil, err
	}
	var out []MLProduct
	for _, p := range all {
		if strings.TrimSpace(p.OfferLink) == "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// Clear limpa todos os dados da planilha mantendo o cabeçalho.
func (c *Client) Clear() error {
	_, err := c.svc.Spreadsheets.Values.Clear(c.spreadsheetID, dataRange, &gsheets.ClearValuesRequest{}).Do()
	if err != nil {
		return fmt.Errorf("sheets: Clear: %w", err)
	}
	return nil
}

// Shuffle lê todos os produtos, embaralha e reescreve na planilha.
func (c *Client) Shuffle() error {
	products, err := c.ReadAllProducts()
	if err != nil {
		return err
	}
	if len(products) == 0 {
		return nil
	}

	rand.Shuffle(len(products), func(i, j int) {
		products[i], products[j] = products[j], products[i]
	})

	if err := c.Clear(); err != nil {
		return err
	}

	rows := make([][]interface{}, len(products))
	for i, p := range products {
		rows[i] = productToRow(p)
	}

	_, err = c.svc.Spreadsheets.Values.Update(
		c.spreadsheetID,
		dataRange,
		&gsheets.ValueRange{Values: rows},
	).ValueInputOption("USER_ENTERED").Do()
	if err != nil {
		return fmt.Errorf("sheets: Shuffle rewrite: %w", err)
	}
	return nil
}

func parseRows(rows [][]interface{}) []MLProduct {
	var products []MLProduct
	for _, row := range rows {
		p := MLProduct{
			ProductName: cellStr(row, 0),
			Price:       cellFloat(row, 1),
			Discount:    cellInt(row, 2),
			Category:    cellStr(row, 3),
			ImageURL:    cellStr(row, 4),
			ProductLink: cellStr(row, 5),
			ShopName:    cellStr(row, 6),
			OfferLink:   cellStr(row, 7),
		}
		if p.ProductName == "" {
			continue
		}
		products = append(products, p)
	}
	return products
}

func productToRow(p MLProduct) []interface{} {
	// Use comma as decimal separator to match the spreadsheet's pt-BR locale.
	// Writing "319.90" with a dot causes Sheets to interpret it as 31990 (dot = thousand separator).
	priceStr := strings.ReplaceAll(fmt.Sprintf("%.2f", p.Price), ".", ",")
	return []interface{}{
		p.ProductName,
		priceStr,
		strconv.Itoa(p.Discount),
		p.Category,
		p.ImageURL,
		p.ProductLink,
		p.ShopName,
		p.OfferLink,
	}
}

func cellStr(row []interface{}, i int) string {
	if i >= len(row) {
		return ""
	}
	return fmt.Sprintf("%v", row[i])
}

func cellFloat(row []interface{}, i int) float64 {
	s := cellStr(row, i)
	// Remove prefixo monetário e espaços (ex: "R$ ", "R$")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "R$")
	s = strings.TrimSpace(s)
	// Remove pontos de milhar e troca vírgula decimal por ponto (formato pt-BR)
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", ".")
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func cellInt(row []interface{}, i int) int {
	s := cellStr(row, i)
	n, _ := strconv.Atoi(s)
	return n
}
