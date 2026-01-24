package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/trogers1052/alert-service/internal/config"
	"github.com/trogers1052/alert-service/internal/kafka"
	"github.com/trogers1052/alert-service/internal/service"
	"github.com/trogers1052/alert-service/internal/telegram"
)

func main() {
	log.Println("Starting alert-service...")

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
	defer consumer.Close()

	// Set up handlers
	consumer.SetDecisionHandler(alertService.HandleDecisionEvent)
	consumer.SetRankingHandler(alertService.HandleRankingEvent)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start consumer
	if err := consumer.Start(ctx); err != nil {
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
	<-sigChan

	log.Println("Shutting down alert-service...")
	cancel()

	// Send shutdown notification
	shutdownCtx := context.Background()
	shutdownMsg := "🛑 <b>Alert Service Stopped</b>"
	if err := telegramClient.SendMessage(shutdownCtx, shutdownMsg); err != nil {
		log.Printf("Warning: failed to send shutdown notification: %v", err)
	}

	log.Println("Alert service stopped")
}
