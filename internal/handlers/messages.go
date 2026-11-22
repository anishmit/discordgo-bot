package handlers

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const (
	messageSyncInterval  = 5 * time.Minute
	channelMessagesLimit = 100
)

var messageSyncOnce sync.Once

func init() {
	registerReadyHandler(messagesReadyHandler)
}

func messagesReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	messageSyncOnce.Do(func() { go runMessageSync(s) })
}

func runMessageSync(s *discordgo.Session) {
	ctx := context.Background()
	for {
		syncMessages(ctx, s)
		time.Sleep(messageSyncInterval)
	}
}

func syncMessages(ctx context.Context, s *discordgo.Session) {
	var afterID int64
	if err := database.Pool.QueryRow(ctx, "SELECT COALESCE(MAX(message_id), 0) FROM messages").Scan(&afterID); err != nil {
		log.Println("Error getting latest message ID", err)
		return
	}

	var inserted int64
	for {
		msgs, err := s.ChannelMessages(channelID, channelMessagesLimit, "", strconv.FormatInt(afterID, 10), "")
		if err != nil {
			log.Println("Error fetching channel messages", err)
			break
		}

		for _, m := range msgs {
			id, err := strconv.ParseInt(m.ID, 10, 64)
			if err != nil {
				log.Println("Error parsing message ID", err)
				continue
			}
			afterID = max(afterID, id)

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
			res, err := database.Pool.Exec(ctx, `
				INSERT INTO messages (message_id, user_id, timestamp_ms, content)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (message_id) DO NOTHING
			`, id, userID, t.UnixMilli(), m.Content)
			if err != nil {
				log.Println("Error inserting into database", err)
				continue
			}
			inserted += res.RowsAffected()
		}

		if len(msgs) < channelMessagesLimit {
			break
		}
	}

	if inserted > 0 {
		log.Printf("Synced %d new message(s)", inserted)
	}
}
