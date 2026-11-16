package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-version" {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		slog.Error("Notifier stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	text, err := loadText(c.Language)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	session, err := discordgo.New("Bot " + c.Token)
	if err != nil {
		return fmt.Errorf("could not configure Discord client")
	}
	session.Client = &http.Client{Timeout: 15 * time.Second}
	session.StateEnabled = false
	discord := newDiscord(session)
	self, err := discord.Self(ctx)
	if err != nil {
		return fmt.Errorf("Discord login: %w", err)
	}
	if !self.Bot {
		return fmt.Errorf("DISCORD_TOKEN must belong to a bot")
	}
	// REST only: no Gateway connection, member cache, privileged intents or inbound listener.
	api := &StatusClient{Client: &http.Client{Timeout: 15 * time.Second}, BaseURL: c.APIURL}
	slog.Info("Notifier started", "version", version, "language", c.Language, "interval", c.Interval, "channels", c.Channels)
	newNotifier(c, text, api, discord, self.ID).Run(ctx)
	slog.Info("Notifier stopped gracefully")
	return nil
}
