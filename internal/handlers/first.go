package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const resultsWindow = 5 * time.Second

var midnightResultsScheduled sync.Map

func snowflakeForTime(t time.Time) string {
	return strconv.FormatInt((t.UnixMilli()-discordEpoch)<<timestampShift, 10)
}

type timePeriod struct {
	name string
	days int
}

const (
	curTimeZone = "America/Los_Angeles"
	guildID     = "407302806241017866"
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
	curLocation *time.Location
)

func init() {
	var err error
	curLocation, err = time.LoadLocation(curTimeZone)
	if err != nil {
		log.Fatalln("Error loading location", err)
	}

	registerCommandHandler("first", firstCommandHandler)
	registerMessageCreateHandler(firstMessageCreateHandler)
}

func firstCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	ctx := context.Background()

	curTime, err := discordgo.SnowflakeTimestamp(i.Interaction.ID)
	if err != nil {
		log.Println("Error getting interaction time", err)
		return
	}
	today := curTime.In(curLocation).Format(time.DateOnly)

	// One row per user, with a count per time period computed in SQL.
	var sb strings.Builder
	sb.WriteString("SELECT user_id")
	for _, tp := range timePeriods {
		if tp.days >= 1e6 {
			sb.WriteString(", count(*)")
		} else {
			fmt.Fprintf(&sb, ", count(*) FILTER (WHERE iso_date >= $1::date - %d)", tp.days-1)
		}
	}
	sb.WriteString(" FROM first_messages WHERE iso_date <= $1::date GROUP BY user_id")

	rows, err := database.Pool.Query(ctx, sb.String(), today)
	if err != nil {
		log.Println("Error reading from database", err)
		return
	}
	defer rows.Close()

	var timePeriodsData [len(timePeriods)]map[int64]int
	for i := range timePeriodsData {
		timePeriodsData[i] = map[int64]int{}
	}
	for rows.Next() {
		var userID int64
		var counts [len(timePeriods)]int
		dest := []any{&userID}
		for i := range counts {
			dest = append(dest, &counts[i])
		}
		if err := rows.Scan(dest...); err != nil {
			log.Println("Error scanning row", err)
			continue
		}
		for i, count := range counts {
			if count > 0 {
				timePeriodsData[i][userID] = count
			}
		}
	}

	fields := make([]*discordgo.MessageEmbedField, 0, len(timePeriods))
	for i, timePeriodData := range timePeriodsData {
		userIDs := make([]int64, 0, len(timePeriodData))
		for userID := range timePeriodData {
			userIDs = append(userIDs, userID)
		}
		sort.Slice(userIDs, func(a, b int) bool {
			return timePeriodData[userIDs[a]] > timePeriodData[userIDs[b]]
		})
		var fieldValue strings.Builder
		for _, userID := range userIDs {
			fmt.Fprintf(&fieldValue, "<@%d>: %d\n", userID, timePeriodData[userID])
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  timePeriods[i].name,
			Value: fieldValue.String(),
		})
	}

	s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{
			{
				Title:  "First Leaderboard",
				Color:  0xff4d01,
				Fields: fields,
			},
		},
	})
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
		dayStart, _ := time.ParseInLocation(time.DateOnly, isoDate, curLocation)
		speed := curTime.UnixMilli() - dayStart.UnixMilli()
		ctx := context.Background()
		msgID, err := strconv.ParseInt(m.ID, 10, 64)
		if err != nil {
			log.Println("Error parsing message id", err)
			return
		}
		userID, err := strconv.ParseInt(m.Author.ID, 10, 64)
		if err != nil {
			log.Println("Error parsing user id", err)
			return
		}
		query := `
			INSERT INTO first_messages (iso_date, content, timestamp_ms, message_id, timezone, user_id, speed)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (iso_date) DO UPDATE
			SET content = EXCLUDED.content,
				timestamp_ms = EXCLUDED.timestamp_ms,
				message_id = EXCLUDED.message_id,
				timezone = EXCLUDED.timezone,
				user_id = EXCLUDED.user_id,
				speed = EXCLUDED.speed
			WHERE EXCLUDED.message_id < first_messages.message_id;
		`
		_, err = database.Pool.Exec(ctx, query, isoDate, m.Content, curTime.UnixMilli(), msgID, curTimeZone, userID, speed)
		if err != nil {
			log.Println("Error executing database insert", err)
		}

		if speed < resultsWindow.Milliseconds() {
			if _, loaded := midnightResultsScheduled.LoadOrStore(isoDate, true); !loaded {
				go runMidnightResults(s, dayStart)
			}
		}
	}
}

func runMidnightResults(s *discordgo.Session, midnight time.Time) {
	fireAt := midnight.Add(resultsWindow)
	time.Sleep(time.Until(fireAt))

	after := snowflakeForTime(midnight.Add(-resultsWindow))
	upperBound := midnight.Add(resultsWindow)

	msgs, err := s.ChannelMessages(channelID, 100, "", after, "")
	if err != nil {
		log.Println("Error fetching results messages", err)
		return
	}

	type entry struct {
		msg    *discordgo.Message
		offset int64
	}
	entries := make([]entry, 0, len(msgs))
	for _, msg := range msgs {
		t, err := discordgo.SnowflakeTimestamp(msg.ID)
		if err != nil {
			continue
		}
		t = t.In(curLocation)
		if t.After(upperBound) {
			continue
		}
		entries = append(entries, entry{msg: msg, offset: t.UnixMilli() - midnight.UnixMilli()})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].offset > entries[j].offset
	})

	winner := -1
	for i, e := range entries {
		if e.offset >= 0 {
			winner = i
		}
	}

	var content strings.Builder
	for i, e := range entries {
		line := fmt.Sprintf("[`%+d ms`](https://discord.com/channels/%s/%s/%s) — <@%s>", e.offset, guildID, channelID, e.msg.ID, e.msg.Author.ID)
		if i == winner {
			line = "# " + line
		}
		content.WriteString(line)
		content.WriteByte('\n')
	}

	if content.Len() == 0 {
		return
	}

	_, err = s.ChannelMessageSend(channelID, content.String())
	if err != nil {
		log.Println("Error sending results message", err)
	}
}
