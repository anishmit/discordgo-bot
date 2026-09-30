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
	geminiModel          = "gemini-3.8-flash"
	maxMsgLength         = 2000
	maxEmbedLength       = 4096
	embedColor           = 0xffffff
	streamEditInterval   = 2 * time.Second
	thoughtsThreadName   = "Thoughts"
	threadArchiveMinutes = 1440
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

type msgRef struct {
	channelID string
	messageID string
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

	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:         "...",
		Reference:       m.Reference(),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		log.Println("Error sending message", err)
		return
	}
	answer := msgRef{channelID: m.ChannelID, messageID: msg.ID}

	var threadID string
	if thread, err := s.MessageThreadStart(m.ChannelID, msg.ID, thoughtsThreadName, threadArchiveMinutes); err != nil {
		log.Println("Error starting thoughts thread", err)
	} else {
		threadID = thread.ID
	}

	text, err := streamResponse(context.Background(), s, prompt, answer, threadID)
	if err != nil {
		log.Println("Error generating response", err)
		editMessage(s, answer, err.Error())
		return
	}
	if text == "" {
		text = "Empty response"
	}
	editMessage(s, answer, text)
}

func isBotMentioned(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	for _, user := range m.Mentions {
		if user.ID == s.State.User.ID {
			return true
		}
	}
	return false
}

func streamResponse(ctx context.Context, s *discordgo.Session, prompt string, answer msgRef, threadID string) (string, error) {
	body := operations.NewCreateInteractionRequestBody(interactions.CreateModelInteraction{
		Model:          interactions.Model(geminiModel),
		Input:          genai.Ptr(interactions.NewInteractionsInput(prompt)),
		SafetySettings: safetySettings,
		Stream:         genai.Ptr(true),
		GenerationConfig: &interactions.GenerationConfig{
			ThinkingSummaries: interactions.ThinkingSummariesAuto.ToPointer(),
		},
	})

	res, err := clients.InteractionsClient.Interactions.Create(ctx, operations.CreateInteractionRequest{Body: body})
	if err != nil {
		return "", err
	}
	stream := res.InteractionSSEStreamEvent
	defer stream.Close()

	var (
		thought             strings.Builder
		currentThoughtIndex = -1
		text                strings.Builder
		lastEdit            = time.Now()
		lastTextLen         int
	)
	defer sendThought(s, threadID, &thought)

	for stream.Next() {
		event := stream.Value()
		if stepDelta := event.GetDataStepDelta(); stepDelta != nil {
			if textDelta := stepDelta.GetDeltaText(); textDelta != nil {
				text.WriteString(textDelta.GetText())
			}
			if thoughtDelta := stepDelta.GetDeltaThoughtSummary(); thoughtDelta != nil {
				if content := thoughtDelta.GetContentText(); content != nil {
					if stepDelta.GetIndex() != currentThoughtIndex {
						sendThought(s, threadID, &thought)
						currentThoughtIndex = stepDelta.GetIndex()
					}
					thought.WriteString(content.GetText())
				}
			}
		}
		if errorEvent := event.GetDataError(); errorEvent != nil {
			if msg := errorEvent.Error.GetMessage(); msg != nil {
				return text.String(), errors.New(*msg)
			}
			return text.String(), errors.New("Stream errored")
		}
		if text.Len() != lastTextLen && time.Since(lastEdit) >= streamEditInterval {
			editMessage(s, answer, text.String())
			lastEdit, lastTextLen = time.Now(), text.Len()
		}
	}
	return text.String(), stream.Err()
}

func sendThought(s *discordgo.Session, threadID string, thought *strings.Builder) {
	if threadID == "" || thought.Len() == 0 {
		return
	}
	if _, err := s.ChannelMessageSend(threadID, capped(thought.String(), maxMsgLength)); err != nil {
		log.Println("Error sending thought", err)
	}
	thought.Reset()
}

func capped(text string, limit int) string {
	return text[:min(len(text), limit)]
}

func editMessage(s *discordgo.Session, ref msgRef, content string) {
	if ref.messageID == "" {
		return
	}
	edit := &discordgo.MessageEdit{
		Content:         &content,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		ID:              ref.messageID,
		Channel:         ref.channelID,
	}
	if len(content) > maxMsgLength {
		empty := ""
		edit.Content = &empty
		edit.Embeds = &[]*discordgo.MessageEmbed{{
			Description: capped(content, maxEmbedLength),
			Color:       embedColor,
		}}
	}
	if _, err := s.ChannelMessageEditComplex(edit); err != nil {
		log.Println("Error editing message", err)
	}
}
