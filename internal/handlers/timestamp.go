package handlers

import (
	"strconv"
	"log"
	"github.com/bwmarrin/discordgo"
)

func init() {
	registerCommandHandler("timestamp", timestampCommandHandler)
}

func timestampCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	mTime, err := discordgo.SnowflakeTimestamp(i.ApplicationCommandData().TargetID)
	if err != nil {
		log.Println("Error getting message time", err)
		return
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: strconv.FormatInt(mTime.UnixMilli(), 10),
		},
	})
}