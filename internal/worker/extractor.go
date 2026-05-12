package worker

import (
	"log/slog"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/gemini"
	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/shopee"
)

type ExtractorConfig struct {
	FilterConfig       shopee.FilterConfig
	ExtractionInterval time.Duration
	FetchLimit         int
}

func RunExtractor(shopeeClient *shopee.Client, geminiClient *gemini.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) {
	run := func() {
		products, err := shopeeClient.FetchProducts(cfg.FilterConfig, cfg.FetchLimit)
		if err != nil {
			log.Error("extractor: FetchProducts", "err", err)
			return
		}

		added, skipped, aiRejected, aiError := 0, 0, 0, 0
		for _, p := range products {
			approved, motivo, err := geminiClient.EvaluateProduct(p.ProductName, p.PriceMin)
			if err != nil {
				log.Error("extractor: EvaluateProduct", "product", p.ProductName, "err", err)
				aiError++
				// falha na IA não bloqueia — enfileira mesmo assim
			} else {
				log.Info("extractor: produto avaliado",
					"title", p.ProductName,
					"aprovado", approved,
					"motivo", motivo,
				)
				if !approved {
					aiRejected++
					continue
				}
			}

			inserted, err := q.Enqueue(queue.Product{
				Title:      p.ProductName,
				Price:      p.PriceMax,
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

		log.Info("extractor: ciclo concluído",
			"adicionados", added,
			"ignorados", skipped,
			"reprovados_ia", aiRejected,
			"erros_ia", aiError,
		)
	}

	run()

	ticker := time.NewTicker(cfg.ExtractionInterval)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}
