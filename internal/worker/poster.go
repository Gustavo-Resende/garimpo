package worker

import (
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/evolution"
	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/scheduler"
	"github.com/Gustavo-Resende/garimpo/internal/telegram"
)

type PosterConfig struct {
	MinInterval       time.Duration
	MaxInterval       time.Duration
	StartHour         int
	EndHour           int
	LowQueueThreshold int
}

func RunPoster(q *queue.Queue, evo *evolution.Client, tg *telegram.Client, cfg PosterConfig, log *slog.Logger) {
	var lastEmptyNotification time.Time
	var lastLowNotification time.Time

	run := func() {
		if !scheduler.IsWithinWindow(cfg.StartHour, cfg.EndHour) {
			log.Info("poster: fora da janela horária, pulando")
			return
		}

		count, err := q.CountPending()
		if err != nil {
			log.Error("poster: CountPending", "err", err)
		} else if count == 0 {
			log.Info("poster: fila vazia, pulando")
			if time.Since(lastEmptyNotification) >= time.Hour {
				if notifyErr := tg.SendNotification("⚠️ Fila vazia — aprove mais produtos no Telegram"); notifyErr != nil {
					log.Warn("poster: SendNotification fila vazia", "err", notifyErr)
				} else {
					lastEmptyNotification = time.Now()
				}
			}
			return
		} else if cfg.LowQueueThreshold > 0 && count <= cfg.LowQueueThreshold {
			log.Info("poster: fila quase vazia", "pending", count)
			if time.Since(lastLowNotification) >= time.Hour {
				msg := fmt.Sprintf("⚠️ Fila quase vazia — restam apenas %d produtos. Aprove mais no Telegram!", count)
				if notifyErr := tg.SendNotification(msg); notifyErr != nil {
					log.Warn("poster: SendNotification fila quase vazia", "err", notifyErr)
				} else {
					lastLowNotification = time.Now()
				}
			}
		}

		product, err := q.Dequeue()
		if err != nil {
			log.Error("poster: Dequeue", "err", err)
			return
		}
		if product == nil {
			log.Info("poster: fila vazia, pulando")
			return
		}

		msg := formatMessage(*product)
		log.Info("poster: mensagem gerada", "id", product.ID, "msg", msg)

		if err := evo.SendMessage(product.ImageURL, msg); err != nil {
			log.Error("poster: SendMessage", "id", product.ID, "err", err)
			if markErr := q.MarkFailed(product.ID); markErr != nil {
				log.Error("poster: MarkFailed", "id", product.ID, "err", markErr)
			}
			return
		}

		if err := q.MarkSent(product.ID); err != nil {
			log.Error("poster: MarkSent", "id", product.ID, "err", err)
			return
		}

		log.Info("poster: produto postado", "id", product.ID, "title", product.Title)
	}

	for {
		run()
		delta := int64(cfg.MaxInterval - cfg.MinInterval)
		interval := cfg.MinInterval + time.Duration(rand.Int63n(delta+1))
		log.Info("poster: próximo post em X minutos", "minutos", int(interval.Minutes()))
		time.Sleep(interval)
	}
}

func formatMessage(p queue.Product) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "🔥 %s\n\n", strings.ToUpper(p.Title))

	if p.Discount > 0 {
		original := p.Price / (1 - float64(p.Discount)/100)
		fmt.Fprintf(&sb, "❌ De ~R$ %s~\n", formatPrice(original))
		fmt.Fprintf(&sb, "✅ Por R$ %s (-%d%%)\n\n", formatPrice(p.Price), p.Discount)
	} else {
		fmt.Fprintf(&sb, "✅ R$ %s\n\n", formatPrice(p.Price))
	}

	if p.Source == "mercadolivre" {
		sb.WriteString("🛒 Oferta verificada no Mercado Livre\n")
	} else {
		sb.WriteString("🛒 Oferta verificada na Shopee\n")
	}
	fmt.Fprintf(&sb, "👉 %s", p.OfferLink)

	return sb.String()
}

func formatPrice(v float64) string {
	return strings.ReplaceAll(fmt.Sprintf("%.2f", v), ".", ",")
}
