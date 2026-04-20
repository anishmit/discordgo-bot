package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/anishmit/discordgo-bot/internal/database"
)

type timePeriod struct {
	name string
	days int
}
type firstMessage struct {
	Content  string
	Time     int64
	MsgID    string
	UserID   string
	TimeZone string
}
type firstMessageSpeed struct {
	speed int64
	date  string
}

const (
	curTimeZone = "America/Los_Angeles"
	serverID    = "407302806241017866"
	channelID   = "407302806241017868"
)

var (
	timePeriods = [5]timePeriod{
		{name: "Today", days: 1},
		{name: "Past Week", days: 7},
		{name: "Past Month", days: 30},
		{name: "Past Year", days: 365},
		{name: "All Time", days: 1e9},
	}
	curLocation        *time.Location
	channelCreatedTime time.Time
	locations          = map[string]*time.Location{}
)

func init() {
	var err error
	curLocation, err = time.LoadLocation(curTimeZone)
	if err != nil {
		log.Fatalln("Error loading location", err)
	}
	locations[curTimeZone] = curLocation

	registerCommandHandler("first", firstCommandHandler)
	registerMessageCreateHandler(firstMessageCreateHandler)
	registerReadyHandler(firstReadyHandler)
}

func firstCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	ctx := context.Background()
	data := make(map[string]firstMessage)
	query := `
		SELECT TO_CHAR(iso_date, 'YYYY-MM-DD'), content, timestamp_ms, message_id, timezone, user_id
		FROM first_messages
	`
	rows, err := database.Pool.Query(ctx, query)
	if err != nil {
		log.Println("Error reading from database", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var dateStr string
		var fm firstMessage
		if err := rows.Scan(&dateStr, &fm.Content, &fm.Time, &fm.MsgID, &fm.TimeZone, &fm.UserID); err != nil {
			log.Println("Error scanning row", err)
			continue
		}
		data[dateStr] = fm
	}

	options := i.ApplicationCommandData().Options

	switch options[0].Name {
	case "count":
		curTime, err := discordgo.SnowflakeTimestamp(i.Interaction.ID)
		if err != nil {
			log.Println("Error getting interaction time", err)
			return
		}
		curTime = curTime.In(curLocation)

		var timePeriodsData [len(timePeriods)]map[string]int
		for i := range timePeriodsData {
			timePeriodsData[i] = map[string]int{}
		}

		daysSubtracted := 0
		for curTime.Year() != channelCreatedTime.Year() || curTime.YearDay() != channelCreatedTime.YearDay() {
			if value, ok := data[curTime.Format(time.DateOnly)]; ok {
				for i, timePeriod := range timePeriods {
					if timePeriod.days > daysSubtracted {
						timePeriodsData[i][value.UserID]++
					}
				}
			}
			curTime = curTime.AddDate(0, 0, -1)
			daysSubtracted++
		}

		fields := make([]*discordgo.MessageEmbedField, 0, len(timePeriods))
		for i, timePeriodData := range timePeriodsData {
			userIDs := make([]string, 0, len(timePeriodData))
			for userID := range timePeriodData {
				userIDs = append(userIDs, userID)
			}
			sort.Slice(userIDs, func(i, j int) bool {
				return timePeriodData[userIDs[i]] > timePeriodData[userIDs[j]]
			})
			var fieldValue string
			for _, userID := range userIDs {
				fieldValue += fmt.Sprintf("<@%s>: %d\n", userID, timePeriodData[userID])
			}
			fields = append(fields, &discordgo.MessageEmbedField{
				Name:  timePeriods[i].name,
				Value: fieldValue,
			})
		}

		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:  "First Leaderboard (Count)",
					Color:  0xff4d01,
					Fields: fields,
				},
			},
		})
	case "time":
		firstMessageSpeeds := make([]firstMessageSpeed, 0, len(data))
		for dateStr, firstMessage := range data {
			var err error
			loc, ok := locations[firstMessage.TimeZone]
			if !ok {
				loc, err = time.LoadLocation(firstMessage.TimeZone)
				if err != nil {
					log.Println("Error loading location", err)
					continue
				}
				locations[firstMessage.TimeZone] = loc
			}

			t, err := time.ParseInLocation(time.DateOnly, dateStr, loc)
			if err != nil {
				log.Println("Error parsing location", err)
				continue
			}

			firstMessageSpeeds = append(firstMessageSpeeds, firstMessageSpeed{
				speed: firstMessage.Time - t.UnixMilli(),
				date:  dateStr,
			})
		}
		sort.Slice(firstMessageSpeeds, func(i, j int) bool {
			if firstMessageSpeeds[i].speed == firstMessageSpeeds[j].speed {
				return firstMessageSpeeds[i].date < firstMessageSpeeds[j].date
			}
			return firstMessageSpeeds[i].speed < firstMessageSpeeds[j].speed
		})
		var description string
		for i := range min(29, len(data)) {
			firstMessage := data[firstMessageSpeeds[i].date]
			description += fmt.Sprintf(
				"%d. <@%s>: **%d** ms on [%s](https://discord.com/channels/%s/%s/%s)\n",
				i+1,
				firstMessage.UserID,
				firstMessageSpeeds[i].speed,
				firstMessageSpeeds[i].date,
				serverID,
				channelID,
				firstMessage.MsgID,
			)
		}
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{
				{
					Title:       "First Leaderboard (Time)",
					Color:       0xff4d01,
					Description: description,
				},
			},
		})
	}
}

func firstMessageCreateHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.ChannelID == channelID {
		curTime, err := discordgo.SnowflakeTimestamp(m.ID)
		if err != nil {
			log.Println("Error getting message time", err)
			return
		}
		curTime = curTime.In(curLocation)
		isoDate := curTime.Format(time.DateOnly)
		ctx := context.Background()
		query := `
			INSERT INTO first_messages (iso_date, content, timestamp_ms, message_id, timezone, user_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (iso_date) DO UPDATE
			SET content = EXCLUDED.content,
				timestamp_ms = EXCLUDED.timestamp_ms,
				message_id = EXCLUDED.message_id,
				timezone = EXCLUDED.timezone,
				user_id = EXCLUDED.user_id
			WHERE EXCLUDED.timestamp_ms < first_messages.timestamp_ms;
		`
		_, err = database.Pool.Exec(ctx, query, isoDate, m.Content, curTime.UnixMilli(), m.ID, curTimeZone, m.Author.ID)
		if err != nil {
			log.Println("Error executing database insert", err)
		}
	}
}

func firstReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	channel, err := s.Channel(channelID)
	if err != nil {
		log.Fatalln("Error getting channel", err)
	}
	channelCreatedTime, err = discordgo.SnowflakeTimestamp(channel.ID)
	if err != nil {
		log.Fatalln("Error getting channel created time", err)
	}
	channelCreatedTime = channelCreatedTime.In(curLocation)
}
