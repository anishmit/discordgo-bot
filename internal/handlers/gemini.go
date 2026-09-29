package handlers

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"

	"github.com/anishmit/discordgo-bot/internal/clients"
)

const (
	geminiModel        = "gemini-3.5-flash-lite"
	maxMsgLength       = 2000
	streamEditInterval = 2 * time.Second
)

var safetySettings = []interactions.SafetySetting{
	{Type: interactions.HarmCategoryHateSpeech, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryDangerousContent, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryHarassment, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategorySexuallyExplicit, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryCivicIntegrity, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryImageHate, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryImageDangerousContent, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryImageHarassment, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryImageSexuallyExplicit, Threshold: interactions.ThresholdOff},
	{Type: interactions.HarmCategoryJailbreak, Threshold: interactions.ThresholdOff},
}

func init() {
	registerMessageCreateHandler(geminiMsgCreateHandler)
}

func geminiMsgCreateHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID || !isBotMentioned(s, m) {
		return
	}

	prompt, err := m.ContentWithMoreMentionsReplaced(s)
	if err != nil {
		log.Println("Error replacing mentions in content", err)
		return
	}
	if prompt == "" {
		return
	}

	msg, err := s.ChannelMessageSend(m.ChannelID, "...")
	if err != nil {
		log.Println("Error sending message", err)
		return
	}

	text, err := streamResponse(context.Background(), s, m.ChannelID, msg.ID, prompt)
	if err != nil {
		log.Println("Error generating response", err)
		editMessage(s, m.ChannelID, msg.ID, err.Error())
		return
	}
	if text == "" {
		text = "Empty response"
	}
	editMessage(s, m.ChannelID, msg.ID, cappedMsg(text))
}

func isBotMentioned(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	for _, user := range m.Mentions {
		if user.ID == s.State.User.ID {
			return true
		}
	}
	return false
}

func streamResponse(ctx context.Context, s *discordgo.Session, channelID, messageID, prompt string) (string, error) {
	body := operations.NewCreateInteractionRequestBody(interactions.CreateModelInteraction{
		Model:          interactions.Model(geminiModel),
		Input:          genai.Ptr(interactions.NewInteractionsInput(prompt)),
		SafetySettings: safetySettings,
		Stream:         genai.Ptr(true),
	})

	res, err := clients.InteractionsClient.Interactions.Create(ctx, operations.CreateInteractionRequest{Body: body})
	if err != nil {
		return "", err
	}
	stream := res.InteractionSSEStreamEvent
	defer stream.Close()

	var (
		text     strings.Builder
		lastEdit = time.Now()
		lastLen  int
	)
	for stream.Next() {
		event := stream.Value()
		if stepDelta := event.GetDataStepDelta(); stepDelta != nil {
			if textDelta := stepDelta.GetDeltaText(); textDelta != nil {
				text.WriteString(textDelta.GetText())
			}
		}
		if errorEvent := event.GetDataError(); errorEvent != nil {
			if msg := errorEvent.Error.GetMessage(); msg != nil {
				return text.String(), errors.New(*msg)
			}
			return text.String(), errors.New("Stream errored")
		}
		if text.Len() != lastLen && time.Since(lastEdit) >= streamEditInterval {
			editMessage(s, channelID, messageID, cappedMsg(text.String()))
			lastEdit, lastLen = time.Now(), text.Len()
		}
	}
	return text.String(), stream.Err()
}

func cappedMsg(text string) string {
	return text[:min(len(text), maxMsgLength)]
}

func editMessage(s *discordgo.Session, channelID, messageID, content string) {
	if _, err := s.ChannelMessageEdit(channelID, messageID, content); err != nil {
		log.Println("Error editing message", err)
	}
}
