package handlers

import (
	"strings"
	"fmt"
	"log"
	"net/http"
	"time"
	"os"
	"github.com/bwmarrin/discordgo"
)

var (
	msgNum = 0
	requestBody = `{"content": "Scheduled message %d sent."}`
	authorizationHeader = fmt.Sprintf("Bot %s", os.Getenv("BOT_TOKEN"))
)

func sendScheduledMessage(channelID string, num int) {
	if req, err := http.NewRequest("POST", fmt.Sprintf("https://discord.com/api/channels/%s/messages", channelID), strings.NewReader(fmt.Sprintf(requestBody, num))); err != nil {
		log.Println("Could not create new HTTP request", err)
	} else {
		req.Header.Add("Authorization", authorizationHeader)
		req.Header.Add("Content-Type", "application/json")
		http.DefaultClient.Do(req)
	}
}

func init() {
	registerCommandHandler("send", sendCommandHandler)
}

func sendCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	sendTime := i.ApplicationCommandData().Options[0].IntValue()
	msgNum++
	num := msgNum
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Scheduled message %d send.", num),
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	time.Sleep(time.Duration(sendTime * 1000000 - time.Now().UnixNano()))
	sendScheduledMessage(i.ChannelID, num)
}