package handlers

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

var (
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){}
	messageCreateHandlers []func(s *discordgo.Session, m *discordgo.MessageCreate)
	messageUpdateHandlers []func(s *discordgo.Session, m *discordgo.MessageUpdate)
	messageDeleteHandlers []func(s *discordgo.Session, m *discordgo.MessageDelete)
	messageDeleteBulkHandlers []func(s *discordgo.Session, m *discordgo.MessageDeleteBulk)
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

func registerMessageUpdateHandler(handler func(s *discordgo.Session, m *discordgo.MessageUpdate)) {
	messageUpdateHandlers = append(messageUpdateHandlers, handler)
}

func registerMessageDeleteHandler(handler func(s *discordgo.Session, m *discordgo.MessageDelete)) {
	messageDeleteHandlers = append(messageDeleteHandlers, handler)
}

func registerMessageDeleteBulkHandler(handler func(s *discordgo.Session, m *discordgo.MessageDeleteBulk)) {
	messageDeleteBulkHandlers = append(messageDeleteBulkHandlers, handler)
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
		name, _, _ := strings.Cut(i.MessageComponentData().CustomID, ":")
		if h, ok := componentHandlers[name]; ok {
			h(s, i)
		}
	}
}

func OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	for _, handler := range messageCreateHandlers {
		handler(s, m)
	}
}

func OnMessageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	for _, handler := range messageUpdateHandlers {
		handler(s, m)
	}
}

func OnMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	for _, handler := range messageDeleteHandlers {
		handler(s, m)
	}
}

func OnMessageDeleteBulk(s *discordgo.Session, m *discordgo.MessageDeleteBulk) {
	for _, handler := range messageDeleteBulkHandlers {
		handler(s, m)
	}
}

func OnReady(s *discordgo.Session, r *discordgo.Ready) {
	for _, handler := range readyHandlers {
		handler(s, r)
	}
}