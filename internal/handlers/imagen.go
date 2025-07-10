package handlers

import (
	"bytes"
	"context"
	"fmt"
	"time"
	"github.com/bwmarrin/discordgo"
	"google.golang.org/genai"
	"github.com/anishmit/discordgo-bot/internal/clients"
)

func init() {
	registerCommandHandler("imagen", imagenCommandHandler)
}

func imagenCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Defer interaction
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Create correct config from options
	options := i.ApplicationCommandData().Options
	optionMap := make(map[string]*discordgo.ApplicationCommandInteractionDataOption, len(options))
	for _, opt := range options {
		optionMap[opt.Name] = opt
	}
	prompt := optionMap["prompt"].StringValue()
	model := "imagen-3.0-generate-002"
	config := &genai.GenerateImagesConfig{
		NumberOfImages: 1,
		PersonGeneration: genai.PersonGenerationAllowAll,
	}
	if option, ok := optionMap["aspect_ratio"]; ok {
		config.AspectRatio = option.StringValue()
	}
	if option, ok := optionMap["model"]; ok {
		model = option.StringValue()
	}

	// Generate image
	ctx := context.Background()
	startTime := time.Now()
	res, err := clients.GeminiClient.Models.GenerateImages(ctx, model, prompt, config)

	// Catch errors and respond to interaction with errors
	if err != nil {
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: fmt.Sprintf(
				"`%s`\n%s", 
				prompt[:min(len(prompt), 1500)],
				err.Error()[:min(len(err.Error()), 500)],
			),
		})
		return
	} else if len(res.GeneratedImages) == 0 {
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: fmt.Sprintf("`%s`\nNo images were generated.", prompt[:min(len(prompt), 1950)]),
		})
		return
	}

	// Respond to interaction with image
	s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Content: fmt.Sprintf(
			"-# %0.1fs\n`%s`", 
			time.Since(startTime).Seconds(),
			prompt[:min(len(prompt), 1950)],
		),
		Files: []*discordgo.File{
			{
				Name: "image.png",
				ContentType: res.GeneratedImages[0].Image.MIMEType,
				Reader: bytes.NewReader(res.GeneratedImages[0].Image.ImageBytes),
			},
		},
	})
}