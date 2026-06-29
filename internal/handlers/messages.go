package handlers

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const messageSyncInterval = 5 * time.Minute

func init() {
	registerReadyHandler(messagesReadyHandler)
}

func messagesReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	go runMessageSync(s)
}

func runMessageSync(s *discordgo.Session) {
	ctx := context.Background()
	for {
		syncMessages(ctx, s)
		time.Sleep(messageSyncInterval)
	}
}

func syncMessages(ctx context.Context, s *discordgo.Session) {
	var latest *int64
	if err := database.Pool.QueryRow(ctx, "SELECT MAX(message_id) FROM messages").Scan(&latest); err != nil {
		log.Println("Error getting latest message ID", err)
		return
	}
	var afterID int64
	if latest != nil {
		afterID = *latest
	}

	inserted := 0
	for {
		msgs, err := s.ChannelMessages(channelID, 100, "", strconv.FormatInt(afterID, 10), "")
		if err != nil {
			log.Println("Error fetching channel messages", err)
			return
		}

		maxID := afterID
		for _, m := range msgs {
			id, err := strconv.ParseInt(m.ID, 10, 64)
			if err != nil {
				log.Println("Error parsing message ID", err)
				continue
			}
			if id > maxID {
				maxID = id
			}

			if m.Author == nil {
				continue
			}

			userID, err := strconv.ParseInt(m.Author.ID, 10, 64)
			if err != nil {
				log.Println("Error parsing user ID", err)
				continue
			}
			t, err := discordgo.SnowflakeTimestamp(m.ID)
			if err != nil {
				log.Println("Error getting message time", err)
				continue
			}
			query := `
				INSERT INTO messages (message_id, user_id, timestamp_ms, content)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (message_id) DO NOTHING;
			`
			res, err := database.Pool.Exec(ctx, query, id, userID, t.UnixMilli(), m.Content)
			if err != nil {
				log.Println("Error inserting into database", err)
				continue
			}
			inserted += int(res.RowsAffected())
		}
		afterID = maxID

		if len(msgs) < 100 {
			break
		}
	}

	if inserted > 0 {
		log.Printf("Synced %d new message(s)", inserted)
	}
}
