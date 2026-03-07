package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/trogers1052/alert-service/internal/config"
	"github.com/trogers1052/alert-service/internal/kafka"
	"github.com/trogers1052/alert-service/internal/service"
	"github.com/trogers1052/alert-service/internal/telegram"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Println("Starting alert-service...")

	// Health endpoint — Docker/systemd HEALTHCHECK target
	healthPort := os.Getenv("HEALTH_PORT")
	if healthPort == "" {
		healthPort = "8080"
	}
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})
	healthServer := &http.Server{
		Addr:              ":" + healthPort,
		Handler:           healthMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Health server error: %v", err)
		}
	}()
	log.Printf("Health endpoint: http://localhost:%s/health", healthPort)

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
	alertService := service.NewAlertService(cfg, telegramClient)

	// Create Kafka consumer
	consumer, err := kafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaConsumerGroup,
		cfg.KafkaDecisionTopic,
		cfg.KafkaRankingTopic,
	)
	if err != nil {
		log.Fatalf("Failed to create Kafka consumer: %v", err)
	}

	// Set up handlers
	consumer.SetDecisionHandler(alertService.HandleDecisionEvent)
	consumer.SetRankingHandler(alertService.HandleRankingEvent)

	// Create context with cancellation — this propagates shutdown to all goroutines
	ctx, cancel := context.WithCancel(context.Background())

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

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	log.Printf("Received signal %v, shutting down alert-service...", sig)

	// Stop accepting new OS signals
	signal.Stop(sigChan)

	// Create a timeout context for the entire shutdown sequence
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	// 1. Cancel the main context — signals all goroutines (including Kafka consumer loop) to stop
	cancel()

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
