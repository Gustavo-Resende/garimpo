package worker

import (
	"log/slog"
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
}

// RunExtractionOnce executa um único ciclo de extração e retorna quantos produtos foram adicionados.
func RunExtractionOnce(shopeeClient *shopee.Client, telegramClient *telegram.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) int {
	totalAdded, totalSkipped := 0, 0

	for page := 1; page <= maxPages; page++ {
		products, hasNextPage, err := shopeeClient.FetchPage(cfg.FilterConfig, cfg.FetchLimit, page)
		if err != nil {
			log.Error("extractor: FetchPage", "page", page, "err", err)
			break
		}

		for _, p := range products {
			if totalAdded >= cfg.TargetQueueSize {
				break
			}

			product := queue.Product{
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
				totalSkipped++
				continue
			}
			totalAdded++

			// Busca o ID gerado pelo banco para usar nos botões do Telegram.
			saved, err := q.GetByOfferLink(p.OfferLink)
			if err != nil || saved == nil {
				log.Error("extractor: GetByOfferLink", "offer_link", p.OfferLink, "err", err)
				continue
			}

			msgID, err := telegramClient.SendProductForReview(*saved)
			if err != nil {
				log.Error("extractor: SendProductForReview", "title", p.ProductName, "err", err)
				continue
			}
			if err := q.SetTelegramMessageID(saved.ID, msgID); err != nil {
				log.Warn("extractor: SetTelegramMessageID", "id", saved.ID, "err", err)
			}
			log.Info("extractor: produto enviado pro Telegram", "title", p.ProductName, "id", saved.ID)
			time.Sleep(2500 * time.Millisecond)
		}

		log.Info("extractor: página processada",
			"page", page,
			"adicionados_acumulado", totalAdded,
			"target", cfg.TargetQueueSize,
		)

		if totalAdded >= cfg.TargetQueueSize || !hasNextPage {
			log.Info("extractor: ciclo concluído",
				"páginas_usadas", page,
				"adicionados", totalAdded,
				"ignorados", totalSkipped,
			)
			break
		}
	}

	return totalAdded
}

func RunExtractor(shopeeClient *shopee.Client, telegramClient *telegram.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) {
	RunExtractionOnce(shopeeClient, telegramClient, q, cfg, log)

	ticker := time.NewTicker(cfg.ExtractionInterval)
	defer ticker.Stop()
	for range ticker.C {
		RunExtractionOnce(shopeeClient, telegramClient, q, cfg, log)
	}
}
