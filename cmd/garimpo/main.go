package main

import (
	"database/sql"
	"log/slog"
	"os"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Gustavo-Resende/garimpo/internal/evolution"
	"github.com/Gustavo-Resende/garimpo/internal/gemini"
	"github.com/Gustavo-Resende/garimpo/internal/queue"
	"github.com/Gustavo-Resende/garimpo/internal/shopee"
	"github.com/Gustavo-Resende/garimpo/internal/worker"
)

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("variável de ambiente obrigatória não definida", "key", key)
		os.Exit(1)
	}
	return v
}

func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Error("variável de ambiente inválida", "key", key, "value", v)
		os.Exit(1)
	}
	return f
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		slog.Error("variável de ambiente inválida", "key", key, "value", v)
		os.Exit(1)
	}
	return i
}

func main() {
	log := slog.Default()

	shopeeAppID      := mustEnv("SHOPEE_APP_ID")
	shopeeSecret     := mustEnv("SHOPEE_SECRET")
	geminiAPIKey     := mustEnv("GEMINI_API_KEY")
	evolutionURL     := mustEnv("EVOLUTION_API_URL")
	evolutionKey     := mustEnv("EVOLUTION_API_KEY")
	evolutionInstance := mustEnv("EVOLUTION_INSTANCE_NAME")
	evolutionGroup   := mustEnv("EVOLUTION_GROUP_JID")
	dbPath           := mustEnv("DB_PATH")

	minCommission   := envFloat("MIN_COMMISSION", 0.08)
	maxCommission   := envFloat("MAX_COMMISSION", 0.40)
	minSales        := envInt("MIN_SALES", 500)
	minRating       := envFloat("MIN_RATING", 4.0)
	extractionHours := envInt("EXTRACTION_INTERVAL_HOURS", 4)
	postingMinutes  := envInt("POSTING_INTERVAL_MINUTES", 12)
	startHour       := envInt("POSTING_START_HOUR", 7)
	endHour         := envInt("POSTING_END_HOUR", 23)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Error("abrir banco de dados", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := queue.Migrate(db); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	q              := queue.NewQueue(db)
	shopeeClient   := shopee.NewClient(shopeeAppID, shopeeSecret)
	geminiClient   := gemini.NewClient(geminiAPIKey)
	evolutionClient := evolution.NewClient(evolutionURL, evolutionKey, evolutionInstance, evolutionGroup)

	extractorCfg := worker.ExtractorConfig{
		FilterConfig: shopee.FilterConfig{
			MinCommission: minCommission,
			MaxCommission: maxCommission,
			MinSales:      minSales,
			MinRating:     minRating,
		},
		ExtractionInterval: time.Duration(extractionHours) * time.Hour,
		FetchLimit:         envInt("SHOPEE_PRODUCT_LIMIT", 50),
	}
	posterCfg := worker.PosterConfig{
		PostingInterval: time.Duration(postingMinutes) * time.Minute,
		StartHour:       startHour,
		EndHour:         endHour,
	}

	log.Info("garimpo iniciando",
		"extraction_interval_hours", extractionHours,
		"posting_interval_minutes", postingMinutes,
		"min_commission", minCommission,
	)

	go worker.RunExtractor(shopeeClient, geminiClient, q, extractorCfg, log)
	go worker.RunPoster(q, geminiClient, evolutionClient, posterCfg, log)

	select {}
}
