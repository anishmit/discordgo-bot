package main

import (
	"os"
	"os/signal"
	"github.com/bwmarrin/discordgo"
	"log"
	"github.com/anishmit/discordgo-bot/internal/handlers"
	"github.com/anishmit/discordgo-bot/internal/commands"
	_ "github.com/joho/godotenv/autoload"
)

var s *discordgo.Session

func init() {
	var err error
	s, err = discordgo.New("Bot " + os.Getenv("BOT_TOKEN"))
	if err != nil {
		log.Fatalf("Invalid bot parameters: %v", err)
	}
}

func main() {
	s.AddHandler(handlers.OnInteractionCreate)
	s.AddHandler(handlers.OnMessageCreate)
	s.AddHandler(handlers.OnReady)

	err := s.Open()
	if err != nil {
		log.Fatalf("Cannot open the session: %v", err)
	}

	commands.UpdateApplicationCommands(s)

	defer s.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Println("Press Ctrl+C to exit")
	<-stop
	log.Println("Gracefully shutting down.")
}
