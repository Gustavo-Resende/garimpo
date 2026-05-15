package worker

import (
	"log/slog"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/gemini"
	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/shopee"
)

const maxPages = 10

type ExtractorConfig struct {
	FilterConfig       shopee.FilterConfig
	ExtractionInterval time.Duration
	FetchLimit         int
	TargetQueueSize    int
}

func RunExtractor(shopeeClient *shopee.Client, geminiClient *gemini.Client, q *queue.Queue, cfg ExtractorConfig, log *slog.Logger) {
	run := func() {
		totalAdded, totalSkipped, totalAIRejected, totalAIError := 0, 0, 0, 0

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

				approved, motivo, err := geminiClient.EvaluateProduct(p.ProductName, p.PriceMin, p.PriceDiscountRate)
				if err != nil {
					log.Error("extractor: EvaluateProduct", "product", p.ProductName, "err", err)
					totalAIError++
					// falha na IA não bloqueia — enfileira mesmo assim
				} else {
					log.Info("extractor: produto avaliado",
						"title", p.ProductName,
						"aprovado", approved,
						"motivo", motivo,
					)
					if !approved {
						totalAIRejected++
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
					totalAdded++
				} else {
					totalSkipped++
				}
			}

			log.Info("extractor: página processada",
				"page", page,
				"aprovados_acumulado", totalAdded,
				"target", cfg.TargetQueueSize,
			)

			if totalAdded >= cfg.TargetQueueSize || !hasNextPage {
				log.Info("extractor: ciclo concluído",
					"páginas_usadas", page,
					"adicionados", totalAdded,
					"ignorados", totalSkipped,
					"reprovados_ia", totalAIRejected,
					"erros_ia", totalAIError,
				)
				break
			}
		}
	}

	run()

	ticker := time.NewTicker(cfg.ExtractionInterval)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}
