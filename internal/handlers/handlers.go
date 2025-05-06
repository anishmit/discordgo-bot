package handlers

import (
	"github.com/bwmarrin/discordgo"
)

var (
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
	messageCreateHandlers []func(s *discordgo.Session, m *discordgo.MessageCreate)
	readyHandlers []func(s *discordgo.Session, r *discordgo.Ready)
)

func registerCommandHandler(name string, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	commandHandlers[name] = handler
}

func registerComponentHandler(name string, handler func(s *discordgo.Session, i *discordgo.InteractionCreate)) {
	componentHandlers[name] = handler
}

func registerMessageCreateHandler(handler func(s *discordgo.Session, m *discordgo.MessageCreate)) {
	messageCreateHandlers = append(messageCreateHandlers, handler)
}

func registerReadyHandler(handler func(s *discordgo.Session, r *discordgo.Ready)) {
	readyHandlers = append(readyHandlers, handler)
}

func OnInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionApplicationCommand {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	} else if i.Type == discordgo.InteractionMessageComponent {
		if h, ok := componentHandlers[i.MessageComponentData().CustomID]; ok {
			h(s, i)
		}
	}
}

func OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	for _, handler := range messageCreateHandlers {
		handler(s, m)
	}
}

func OnReady(s *discordgo.Session, r *discordgo.Ready) {
	for _, handler := range readyHandlers {
		handler(s, r)
	}
}