package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/trogers1052/alert-service/internal/config"
	"github.com/trogers1052/alert-service/internal/kafka"
	"github.com/trogers1052/alert-service/internal/service"
	"github.com/trogers1052/trading-go-commons/httpserver"
	"github.com/trogers1052/trading-go-commons/telegram"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Println("Starting alert-service...")

	// Health endpoint — Docker/systemd HEALTHCHECK target
	healthPort := os.Getenv("HEALTH_PORT")
	if healthPort == "" {
		healthPort = "8080"
	}
	healthServer := httpserver.NewHealthServer(":" + healthPort)
	healthErrCh := healthServer.Start()
	go func() {
		if err := <-healthErrCh; err != nil {
			log.Printf("Health server error: %v", err)
		}
	}()
	log.Printf("Health endpoint: http://localhost:%s/health", healthPort)

	// Metrics endpoint — Prometheus scrape target
	startMetricsServer()

	// Create Prometheus metrics recorder — injected into internal packages
	metricsRecorder := newPromRecorder()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Configuration loaded:")
	log.Printf("  Kafka brokers: %v", cfg.KafkaBrokers)
	log.Printf("  Decision topic: %s", cfg.KafkaDecisionTopic)
	log.Printf("  Ranking topic: %s", cfg.KafkaRankingTopic)
	log.Printf("  Min confidence: %.2f", cfg.MinConfidence)
	log.Printf("  Alert on BUY: %v, SELL: %v, WATCH: %v",
		cfg.AlertOnBuy, cfg.AlertOnSell, cfg.AlertOnWatch)
	log.Printf("  Cooldown: %d minutes", cfg.CooldownMinutes)

	// Create Telegram client
	telegramClient := telegram.NewClient(cfg.TelegramBotToken, cfg.TelegramChatID)

	// Create alert service
	alertService := service.NewAlertService(cfg, telegramClient, metricsRecorder)

	// Create Kafka consumer
	consumer, err := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaConsumerGroup,
		cfg.KafkaDecisionTopic,
		cfg.KafkaRankingTopic,
		metricsRecorder,
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka consumer: %v", err)
	}

	// Set up handlers
	consumer.SetDecisionHandler(alertService.HandleDecisionEvent)
	consumer.SetRankingHandler(alertService.HandleRankingEvent)

	// Create a context cancelled on SIGINT/SIGTERM — this propagates shutdown to
	// all goroutines (including the Kafka consumer loop).
	ctx, cancel := httpserver.SignalContext()
	defer cancel()

	// Start consumer
	if err := consumer.Start(ctx); err != nil {
		cancel()
		log.Fatalf("Failed to start Kafka consumer: %v", err)
	}

	log.Println("Alert service running. Waiting for messages...")

	// Send startup notification
	startupMsg := "🚀 <b>Alert Service Started</b>\n\nNow monitoring for trading signals."
	if err := telegramClient.SendMessage(ctx, startupMsg); err != nil {
		log.Printf("Warning: failed to send startup notification: %v", err)
	}

	// Wait for shutdown signal (ctx is cancelled on SIGINT/SIGTERM)
	<-ctx.Done()

	log.Printf("Received shutdown signal, shutting down alert-service...")

	// 1. Cancel the main context — signals all goroutines (including the Kafka
	// consumer loop) to stop and stops intercepting OS signals.
	cancel()

	// Create a timeout context for the entire shutdown sequence
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// 2. Close the Kafka consumer — waits for the consumer goroutine to exit, then closes the client
	if err := consumer.Close(); err != nil {
		log.Printf("Warning: error closing Kafka consumer: %v", err)
	}
	log.Println("Kafka consumer closed")

	// 3. Stop the alert service background goroutines (cooldown cleanup)
	alertService.Close()
	log.Println("Alert service closed")

	// 4. Send shutdown notification (uses shutdownCtx so it has its own timeout)
	shutdownMsg := "🛑 <b>Alert Service Stopped</b>"
	if err := telegramClient.SendMessage(shutdownCtx, shutdownMsg); err != nil {
		log.Printf("Warning: failed to send shutdown notification: %v", err)
	}

	// 5. Close the Telegram client (release idle HTTP connections)
	telegramClient.Close()
	log.Println("Telegram client closed")

	// 6. Shut down the health HTTP server
	if err := healthServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Warning: error shutting down health server: %v", err)
	}
	log.Println("Health server closed")

	log.Println("Alert service stopped cleanly")
}
