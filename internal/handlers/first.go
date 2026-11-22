package handlers

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const (
	curTimeZone   = "America/Los_Angeles"
	guildID       = "407302806241017866"
	channelID     = "407302806241017868"
	resultsWindow = 5 * time.Second
)

type timePeriod struct {
	name string
	days int
}

type firstCount struct {
	userID int64
	count  int
}

var (
	curLocation = loadLocation(curTimeZone)
	timePeriods = []timePeriod{
		{name: "Today", days: 1},
		{name: "Past Week", days: 7},
		{name: "Past Month", days: 30},
		{name: "Past Year", days: 365},
		{name: "All Time"},
	}
	midnightResultsScheduled sync.Map
)

func init() {
	registerCommandHandler("first", firstCommandHandler)
	registerMessageCreateHandler(firstMessageCreateHandler)
}

func snowflakeForTime(t time.Time) string {
	return strconv.FormatInt((t.UnixMilli()-discordEpoch)<<timestampShift, 10)
}

func firstCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	now, err := discordgo.SnowflakeTimestamp(i.ID)
	if err != nil {
		followupError(s, i, "Error getting interaction time", err)
		return
	}
	leaderboards, err := firstLeaderboards(now)
	if err != nil {
		followupError(s, i, "Error reading from database", err)
		return
	}

	fields := make([]*discordgo.MessageEmbedField, len(timePeriods))
	for p, leaderboard := range leaderboards {
		var value strings.Builder
		for _, c := range leaderboard {
			fmt.Fprintf(&value, "<@%d>: %d\n", c.userID, c.count)
		}
		fields[p] = &discordgo.MessageEmbedField{Name: timePeriods[p].name, Value: value.String()}
	}
	followup(s, i, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{{
			Title:  "First Leaderboard",
			Color:  0xff4d01,
			Fields: fields,
		}},
	})
}

func firstLeaderboards(now time.Time) ([][]firstCount, error) {
	query := "SELECT user_id"
	for _, tp := range timePeriods {
		if tp.days == 0 {
			query += ", count(*)"
		} else {
			query += fmt.Sprintf(", count(*) FILTER (WHERE iso_date > $1::date - %d)", tp.days)
		}
	}
	query += " FROM first_messages WHERE iso_date <= $1::date GROUP BY user_id"

	rows, err := database.Pool.Query(context.Background(), query, now.In(curLocation).Format(time.DateOnly))
	if err != nil {
		return nil, err
	}

	var userID int64
	counts := make([]int, len(timePeriods))
	scans := []any{&userID}
	for p := range counts {
		scans = append(scans, &counts[p])
	}
	leaderboards := make([][]firstCount, len(timePeriods))
	_, err = pgx.ForEachRow(rows, scans, func() error {
		for p, count := range counts {
			if count > 0 {
				leaderboards[p] = append(leaderboards[p], firstCount{userID, count})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, leaderboard := range leaderboards {
		slices.SortFunc(leaderboard, func(a, b firstCount) int {
			return cmp.Compare(b.count, a.count)
		})
	}
	return leaderboards, nil
}

func firstMessageCreateHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.ChannelID != channelID {
		return
	}
	sentAt, err := discordgo.SnowflakeTimestamp(m.ID)
	if err != nil {
		log.Println("Error getting message time", err)
		return
	}
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

	sentAt = sentAt.In(curLocation)
	year, month, day := sentAt.Date()
	midnight := time.Date(year, month, day, 0, 0, 0, 0, curLocation)
	isoDate := midnight.Format(time.DateOnly)
	speed := sentAt.Sub(midnight)

	_, err = database.Pool.Exec(context.Background(), `
		INSERT INTO first_messages (iso_date, content, timestamp_ms, message_id, timezone, user_id, speed)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (iso_date) DO UPDATE
		SET content = EXCLUDED.content,
			timestamp_ms = EXCLUDED.timestamp_ms,
			message_id = EXCLUDED.message_id,
			timezone = EXCLUDED.timezone,
			user_id = EXCLUDED.user_id,
			speed = EXCLUDED.speed
		WHERE EXCLUDED.message_id < first_messages.message_id
	`, isoDate, m.Content, sentAt.UnixMilli(), msgID, curTimeZone, userID, speed.Milliseconds())
	if err != nil {
		log.Println("Error executing database insert", err)
	}

	if speed < resultsWindow {
		if _, scheduled := midnightResultsScheduled.LoadOrStore(isoDate, true); !scheduled {
			go runMidnightResults(s, midnight)
		}
	}
}

func runMidnightResults(s *discordgo.Session, midnight time.Time) {
	end := midnight.Add(resultsWindow)
	time.Sleep(time.Until(end))

	msgs, err := s.ChannelMessages(channelID, channelMessagesLimit, "", snowflakeForTime(midnight.Add(-resultsWindow)), "")
	if err != nil {
		log.Println("Error fetching results messages", err)
		return
	}

	type result struct {
		msg    *discordgo.Message
		offset time.Duration
	}
	var results []result
	for _, msg := range msgs {
		t, err := discordgo.SnowflakeTimestamp(msg.ID)
		if err != nil || t.After(end) {
			continue
		}
		results = append(results, result{msg, t.Sub(midnight)})
	}
	if len(results) == 0 {
		return
	}
	slices.SortFunc(results, func(a, b result) int {
		return cmp.Compare(b.offset, a.offset)
	})

	winner := -1
	for i, r := range results {
		if r.offset >= 0 {
			winner = i
		}
	}

	var content strings.Builder
	for i, r := range results {
		if i == winner {
			content.WriteString("# ")
		}
		fmt.Fprintf(&content, "[`%+d ms`](https://discord.com/channels/%s/%s/%s) — <@%s>\n", r.offset.Milliseconds(), guildID, channelID, r.msg.ID, r.msg.Author.ID)
	}
	if _, err := s.ChannelMessageSend(channelID, content.String()); err != nil {
		log.Println("Error sending results message", err)
	}
}
