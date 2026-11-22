package commands

import (
	"github.com/bwmarrin/discordgo"
)

const devGuildID = "1219548619129225226"

var devCommands = []*discordgo.ApplicationCommand{}

var globalCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "first",
		Description: "Leaderboard for number of first messages",
	},
	{
		Name: "Timestamp",
		Type: discordgo.MessageApplicationCommand,
	},
	{
		Name:        "ud",
		Description: "Search Urban Dictionary",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "term",
				Description: "Term to search for",
				Required:    true,
				MaxLength:   80,
			},
		},
	},
	{
		Name:        "latex",
		Description: "LaTeX game",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "answer",
				Description: "Answer LaTeX problem",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "latex",
						Description: "LaTeX to render",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "problem",
				Description: "Get new LaTeX problem",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "leaderboard",
				Description: "Get LaTeX leaderboard",
			},
		},
	},
	{
		Name:        "credits",
		Description: "Show Google Cloud credits for the bot's billing account",
	},
	{
		Name:        "gemini",
		Description: "Configure Gemini settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "settings",
				Description: "Change Gemini settings",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "clear",
				Description: "Clear the history",
			},
		},
	},
	{
		Name:        "yt",
		Description: "Play YouTube videos in a voice channel",
		Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "play",
				Description: "Search and play YouTube videos",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "query",
						Description: "Search query or YouTube link",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "next",
				Description: "Play the next video in the queue",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "queue",
				Description: "View the current queue",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "move",
				Description: "Move the bot to your voice channel",
			},
		},
	},
}

func UpdateApplicationCommands(s *discordgo.Session) error {
	_, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, devGuildID, devCommands)
	if err != nil {
		return err
	}
	_, err = s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", globalCommands)
	if err != nil {
		return err
	}
	return nil
}
