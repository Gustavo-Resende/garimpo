package worker

import (
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/shopee"
	"github.com/Gustavo-Resende/garimpo/internal/telegram"
)

const maxPages = 10

type ExtractorConfig struct {
	FilterConfig       shopee.FilterConfig
	ExtractionInterval time.Duration
	FetchLimit         int
	TargetQueueSize    int
	SearchKeywords     []string
	MaxPerKeyword      int
}

// RunExtractionOnce executa um único ciclo de extração e retorna quantos produtos foram adicionados.
func RunExtractionOnce(shopeeClient *shopee.Client, telegramClient *telegram.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) int {
	maxPerKeyword := cfg.MaxPerKeyword
	if maxPerKeyword <= 0 {
		maxPerKeyword = 5
	}

	if len(cfg.SearchKeywords) == 0 {
		added, skipped, _ := fetchKeyword(shopeeClient, telegramClient, q, cfg, log, "", cfg.TargetQueueSize)
		log.Info("extractor: ciclo concluído", "adicionados", added, "ignorados", skipped)
		return added
	}

	totalAdded, totalSkipped, totalFiltered := 0, 0, 0
	keywordsUsed := 0

	for _, keyword := range cfg.SearchKeywords {
		if totalAdded >= cfg.TargetQueueSize {
			break
		}

		limit := maxPerKeyword
		if remaining := cfg.TargetQueueSize - totalAdded; remaining < limit {
			limit = remaining
		}

		added, skipped, filtered := fetchKeyword(shopeeClient, telegramClient, q, cfg, log, keyword, limit)
		log.Info("extractor: keyword concluída", "keyword", keyword, "adicionados", added)

		totalAdded += added
		totalSkipped += skipped
		totalFiltered += filtered
		if added > 0 {
			keywordsUsed++
		}
	}

	log.Info("extractor: ciclo concluído",
		"keywords_usadas", keywordsUsed,
		"adicionados", totalAdded,
		"ignorados", totalSkipped,
		"filtrados", totalFiltered,
	)
	return totalAdded
}

// fetchKeyword percorre páginas para uma keyword (ou sem keyword se vazia) e enfileira até maxAdd produtos.
// Retorna (adicionados, ignorados, filtrados).
// ignorados = vistos antes ou já na fila. filtrados = não processados por ter atingido o limite.
func fetchKeyword(shopeeClient *shopee.Client, telegramClient *telegram.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger, keyword string, maxAdd int) (added, skipped, filtered int) {
	for page := 1; page <= maxPages; page++ {
		products, hasNextPage, err := shopeeClient.FetchPage(cfg.FilterConfig, cfg.FetchLimit, page, keyword)
		if err != nil {
			log.Error("extractor: FetchPage", "keyword", keyword, "page", page, "err", err)
			break
		}

		for _, p := range products {
			if added >= maxAdd {
				filtered += len(products) // conta restantes da página como filtrados
				break
			}

			if p.ItemID != 0 {
				seen, err := q.IsSeenItem(p.ItemID)
				if err != nil {
					log.Error("extractor: IsSeenItem", "item_id", p.ItemID, "err", err)
				} else if seen {
					log.Debug("extractor: skip itemId já visto", "item_id", p.ItemID)
					skipped++
					continue
				}
			}

			product := queue.Product{
				ItemID:     p.ItemID,
				Title:      p.ProductName,
				Price:      p.PriceMax,
				Discount:   p.PriceDiscountRate,
				Commission: p.CommissionRate,
				ImageURL:   p.ImageURL,
				OfferLink:  p.OfferLink,
				Source:     "shopee",
			}

			inserted, err := q.Enqueue(product)
			if err != nil {
				log.Error("extractor: Enqueue", "product", p.ProductName, "err", err)
				continue
			}
			if !inserted {
				skipped++
				continue
			}

			if p.ItemID != 0 {
				if err := q.MarkSeenItem(p.ItemID); err != nil {
					log.Warn("extractor: MarkSeenItem", "item_id", p.ItemID, "err", err)
				}
			}
			added++

			saved, err := q.GetByOfferLink(p.OfferLink)
			if err != nil || saved == nil {
				log.Error("extractor: GetByOfferLink", "offer_link", p.OfferLink, "err", err)
				continue
			}

			msgID, err := sendWithRetry(telegramClient, *saved, log)
			if err != nil {
				log.Warn("extractor: produto descartado após 3 tentativas", "title", p.ProductName, "err", err)
				continue
			}
			if err := q.SetTelegramMessageID(saved.ID, msgID); err != nil {
				log.Warn("extractor: SetTelegramMessageID", "id", saved.ID, "err", err)
			}
			log.Info("extractor: produto enviado pro Telegram", "title", p.ProductName, "id", saved.ID)
			time.Sleep(500 * time.Millisecond)
		}

		if added >= maxAdd || !hasNextPage {
			break
		}
	}
	return added, skipped, filtered
}

// sendWithRetry tenta SendProductForReview até 3 vezes.
// Em caso de Too Many Requests, dorme o tempo indicado pelo Telegram + 1s antes de tentar novamente.
func sendWithRetry(client *telegram.Client, p queue.Product, log *slog.Logger) (int, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := range maxAttempts {
		msgID, err := client.SendProductForReview(p)
		if err == nil {
			return msgID, nil
		}
		lastErr = err

		if secs := retryAfterSeconds(err.Error()); secs > 0 {
			wait := time.Duration(secs+1) * time.Second
			log.Warn("extractor: rate limit Telegram, aguardando", "attempt", attempt+1, "wait_seconds", secs+1)
			time.Sleep(wait)
		}
	}
	return 0, lastErr
}

// retryAfterSeconds extrai o número de segundos de um erro "Too Many Requests: retry after X".
// Retorna 0 se o erro não for desse tipo.
func retryAfterSeconds(errMsg string) int {
	const marker = "retry after "
	idx := strings.Index(errMsg, marker)
	if idx == -1 {
		return 0
	}
	rest := errMsg[idx+len(marker):]
	// pega apenas os dígitos iniciais
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end != -1 {
		rest = rest[:end]
	}
	secs, err := strconv.Atoi(rest)
	if err != nil {
		return 0
	}
	return secs
}

func RunExtractor(shopeeClient *shopee.Client, telegramClient *telegram.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) {
	RunExtractionOnce(shopeeClient, telegramClient, q, cfg, log)

	ticker := time.NewTicker(cfg.ExtractionInterval)
	defer ticker.Stop()
	for range ticker.C {
		RunExtractionOnce(shopeeClient, telegramClient, q, cfg, log)
	}
}
