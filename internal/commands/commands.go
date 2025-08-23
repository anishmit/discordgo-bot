package commands

import (
	"github.com/bwmarrin/discordgo"
)

const devGuildID = "1219548619129225226"

var devCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "yt",
		Description: "Play YouTube video",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "query",
				Description: "Search query",
				Required:    true,
			},
		},
	},
}

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
		Name:        "imagen",
		Description: "Generate an image with Imagen",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "prompt",
				Description: "Prompt used for generated image",
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "aspect_ratio",
				Description: "Aspect ratio used for generated image",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{
						Name:  "1:1",
						Value: "1:1",
					},
					{
						Name:  "9:16",
						Value: "9:16",
					},
					{
						Name:  "16:9",
						Value: "16:9",
					},
					{
						Name:  "3:4",
						Value: "3:4",
					},
					{
						Name:  "4:3",
						Value: "4:3",
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "model",
				Description: "Model used to generate image",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{
						Name:  "Imagen 4",
						Value: "imagen-4.0-generate-001",
					},
					{
						Name:  "Imagen 4 Ultra",
						Value: "imagen-4.0-ultra-generate-001",
					},
					{
						Name:  "Imagen 4 Fast Generate",
						Value: "imagen-4.0-fast-generate-001",
					},
					{
						Name:  "Imagen 3",
						Value: "imagen-3.0-generate-002",
					},
				},
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
				Name:        "grounding",
				Description: "Toggle Grounding with Google Search",
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
								Name:  "Gemini 2.5 Pro",
								Value: "gemini-2.5-pro",
							},
							{
								Name:  "Gemini 2.5 Flash ",
								Value: "gemini-2.5-flash",
							},
						},
					},
				},
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
