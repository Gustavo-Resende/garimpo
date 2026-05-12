package worker

import (
	"log/slog"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/shopee"
)

type ExtractorConfig struct {
	FilterConfig       shopee.FilterConfig
	ExtractionInterval time.Duration
	FetchLimit         int
}

func RunExtractor(client *shopee.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) {
	run := func() {
		products, err := client.FetchProducts(cfg.FilterConfig, cfg.FetchLimit)
		if err != nil {
			log.Error("extractor: FetchProducts", "err", err)
			return
		}

		added, skipped := 0, 0
		for _, p := range products {
			inserted, err := q.Enqueue(queue.Product{
				Title:      p.ProductName,
				Price:      p.PriceMin,
				Discount:   p.PriceDiscountRate,
				Commission: p.CommissionRate,
				ImageURL:   p.ImageURL,
				OfferLink:  p.OfferLink,
				Source:     "shopee",
			})
			if err != nil {
				log.Error("extractor: Enqueue", "product", p.ProductName, "err", err)
				continue
			}
			if inserted {
				added++
			} else {
				skipped++
			}
		}

		log.Info("extractor: ciclo concluído", "adicionados", added, "ignorados", skipped)
	}

	run()

	ticker := time.NewTicker(cfg.ExtractionInterval)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}
