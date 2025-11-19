package handlers

import (
	"fmt"
	"log"
	"strconv"
	"github.com/bwmarrin/discordgo"
)

const (
	discordEpoch   = 1420070400000
	timestampShift = 22
	workerIdShift  = 17
	processIDShift = 12
	workerIDMask   = 0x1F
	processIDMask  = 0x1F
	incrementMask  = 0xFFF
)

func init() {
	registerCommandHandler("Timestamp", timestampCommandHandler)
}

func timestampCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id, err := strconv.ParseUint(i.ApplicationCommandData().TargetID, 10, 64)
	if err != nil {
		log.Println("Error converting message ID to int", err)
		return
	}

	timestamp := (id >> timestampShift) + discordEpoch
	workerID := (id >> workerIdShift) & workerIDMask
	processID := (id >> processIDShift) & processIDMask
	increment := id & incrementMask

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Timestamp: `%d`\nWorker ID: `%d`\nProcess ID: `%d`\nIncrement: `%d`", timestamp, workerID, processID, increment),
		},
	})
}
