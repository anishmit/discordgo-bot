package commands

import (
	"github.com/bwmarrin/discordgo"
)

const devGuildID = "1219548619129225226"

var devCommands = []*discordgo.ApplicationCommand{}

var globalCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "first",
		Description: "Data about first messages",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "count",
				Description: "Leaderboard for number of first messages",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
			{
				Name:        "time",
				Description: "Leaderboard for fastest first messages",
				Type:        discordgo.ApplicationCommandOptionSubCommand,
			},
		},
	},
	{
		Name:        "send",
		Description: "Schedule sending a message at a Unix epoch time in milliseconds",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "time",
				Description: "Unix epoch time in milliseconds",
				Required:    true,
			},
		},
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
		Name:        "gemini",
		Description: "Configure Gemini settings",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "search",
				Description: "Toggle Google Search",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "clear",
				Description: "Clear history for the current channel",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "markdown",
				Description: "Toggle markdown rendering for every response",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "code",
				Description: "Toggle code execution",
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "model",
				Description: "Change model",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "name",
						Description: "Model name",
						Required:    true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{
								Name:  "Gemini 3 Pro",
								Value: "gemini-3-pro-preview",
							},
							{
								Name:  "Gemini 3 Pro Image Preview",
								Value: "gemini-3-pro-image-preview",
							},
							{
								Name:  "Gemini 3 Flash",
								Value: "gemini-3-flash-preview",
							},
							{
								Name:  "Gemini 2.5 Pro",
								Value: "gemini-2.5-pro",
							},
							{
								Name:  "Gemini 2.5 Flash",
								Value: "gemini-2.5-flash",
							},
							{
								Name: "Gemini 2.5 Pro TTS",
								Value: "gemini-2.5-pro-preview-tts",
							},
							{
								Name: "Gemini 2.5 Flash TTS",
								Value: "gemini-2.5-flash-preview-tts",
							},
						},
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "thinking",
				Description: "Change thinking level (for Gemini 3 models)",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "name",
						Description: "Model name",
						Required:    true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{
								Name:  "Low",
								Value: "LOW",
							},
							{
								Name:  "High",
								Value: "HIGH",
							},
						},
					},
				},
			},
		},
	},
	{
		Name:        "yt",
		Description: "Play YouTube videos in a voice channel",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "play",
				Description: "Search and play YouTube videos",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "query",
						Description: "Search query",
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
