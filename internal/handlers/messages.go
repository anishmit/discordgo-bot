package handlers

import (
	"github.com/bwmarrin/discordgo"
)

func init() {
	registerReadyHandler(messagesReadyHandler)
}

func messagesReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	go populateMessages(s)
}

func populateMessages(s *discordgo.Session) {
	/*
	ctx := context.Background()

	// Resume from the latest message we already have, or start from the
	// beginning (afterID = "1") if the table is empty. message_id is stored
	// as text, so cast to bigint for a correct numeric max (snowflakes in
	// this channel span the 18- to 19-digit boundary).
	afterID := "1"
	var latest *int64
	if err := database.Pool.QueryRow(ctx, "SELECT MAX(message_id::bigint) FROM messages").Scan(&latest); err != nil {
		log.Println("Error getting latest message id", err)
		return
	}
	if latest != nil {
		afterID = strconv.FormatInt(*latest, 10)
	}

	total := 0
	for {
		msgs, err := s.ChannelMessages(channelID, 100, "", afterID, "")
		if err != nil {
			log.Println("Error fetching channel messages", err)
			return
		}
		if len(msgs) == 0 {
			break
		}
		total += len(msgs)
		log.Printf("Fetched %d messages so far", total)

		// Advance afterID to the highest (newest) message ID in the batch.
		// The API may return the batch newest-first, so track the max
		// numerically rather than assuming order.
		var maxID int64
		if id, err := strconv.ParseInt(afterID, 10, 64); err == nil {
			maxID = id
		}

		for _, m := range msgs {
			// Always advance past this message, even if we skip inserting it,
			// so a batch of only webhook messages can't stall pagination.
			if id, err := strconv.ParseInt(m.ID, 10, 64); err == nil && id > maxID {
				maxID = id
			}

			// Webhook-sent messages do not have a full author.
			if m.Author == nil {
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
			if _, err := database.Pool.Exec(ctx, query, m.ID, m.Author.ID, t.UnixMilli(), m.Content); err != nil {
				log.Println("Error executing database insert", err)
				continue
			}
		}
		afterID = strconv.FormatInt(maxID, 10)

		if len(msgs) < 100 {
			break
		}
	}
	*/
}
