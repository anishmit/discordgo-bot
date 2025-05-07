package handlers

import (
	"github.com/bwmarrin/discordgo"
)

func init() {
	registerCommandHandler("latex", latexCommandHandler)
}

func latexCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	
}