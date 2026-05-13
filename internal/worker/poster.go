package worker

import (
	"log/slog"
	"math/rand"
	"time"

	"github.com/Gustavo-Resende/garimpo/internal/evolution"
	"github.com/Gustavo-Resende/garimpo/internal/gemini"
	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/scheduler"
)

type PosterConfig struct {
	MinInterval time.Duration
	MaxInterval time.Duration
	StartHour   int
	EndHour     int
}

func RunPoster(q *queue.Queue, gem *gemini.Client, evo *evolution.Client, cfg PosterConfig, log *slog.Logger) {
	run := func() {
		if !scheduler.IsWithinWindow(cfg.StartHour, cfg.EndHour) {
			log.Info("poster: fora da janela horária, pulando")
			return
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

		msg, err := gem.GenerateMessage(*product)
		if err != nil {
			log.Error("poster: GenerateMessage", "id", product.ID, "err", err)
			if markErr := q.MarkFailed(product.ID); markErr != nil {
				log.Error("poster: MarkFailed", "id", product.ID, "err", markErr)
			}
			return
		}

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
