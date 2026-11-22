package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const (
	logsColor      = 0x5865F2
	logsPagePrefix = "logs"
	logsTimeout    = 2500 * time.Millisecond
	logsHeading    = "## Logs"
	logsNoText     = "-# No text"
	// Components V2 messages allow 40 components, counting nested ones, and 4000 characters of text.
	logsMaxComponents = 40
	logsMaxText       = 4000
	// Around its entries, a page has a container, a heading, a footer, an action row and two buttons.
	logsPageComponents = 6
	logsFooterLength   = 100
)

const (
	createLogsSQL = `CREATE TABLE IF NOT EXISTS tracked_messages (
	message_id bigint PRIMARY KEY,
	channel_id bigint NOT NULL,
	user_id bigint NOT NULL,
	content text NOT NULL
);
CREATE TABLE IF NOT EXISTS message_logs (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	channel_id bigint NOT NULL,
	message_id bigint NOT NULL,
	user_id bigint NOT NULL,
	timestamp_ms bigint NOT NULL,
	old_content text NOT NULL,
	new_content text
);
CREATE INDEX IF NOT EXISTS message_logs_channel_id_id_idx ON message_logs (channel_id, id)`
	trackMessageSQL = `INSERT INTO tracked_messages (message_id, channel_id, user_id, content) VALUES ($1, $2, $3, $4) ON CONFLICT (message_id) DO NOTHING`
	// Every part of the statement sees the table as it was before the statement, so old holds the content from before the edit.
	logEditSQL = `WITH old AS (
	SELECT content FROM tracked_messages WHERE message_id = $1 FOR UPDATE
), saved AS (
	INSERT INTO tracked_messages (message_id, channel_id, user_id, content) VALUES ($1, $2, $3, $4)
	ON CONFLICT (message_id) DO UPDATE SET content = EXCLUDED.content WHERE tracked_messages.content <> EXCLUDED.content
)
INSERT INTO message_logs (channel_id, message_id, user_id, timestamp_ms, old_content, new_content)
SELECT $2, $1, $3, $5::bigint, content, $4 FROM old WHERE content <> $4`
	logDeletesSQL = `WITH deleted AS (
	DELETE FROM tracked_messages WHERE message_id = ANY($1) RETURNING message_id, channel_id, user_id, content
)
INSERT INTO message_logs (channel_id, message_id, user_id, timestamp_ms, old_content)
SELECT channel_id, message_id, user_id, $2::bigint, content FROM deleted ORDER BY message_id`
	selectLogsSQL = `SELECT id, message_id, user_id, timestamp_ms, old_content, new_content FROM message_logs WHERE channel_id = $1 AND id <= $2 ORDER BY id DESC`
)

type messageLog struct {
	ID         int64
	MessageID  int64
	UserID     int64
	Timestamp  int64
	OldContent string
	// NewContent is nil when the message was deleted.
	NewContent *string
}

func init() {
	registerReadyHandler(logsReadyHandler)
	registerMessageCreateHandler(logsMessageCreateHandler)
	registerMessageUpdateHandler(logsMessageUpdateHandler)
	registerMessageDeleteHandler(logsMessageDeleteHandler)
	registerMessageDeleteBulkHandler(logsMessageDeleteBulkHandler)
	registerCommandHandler("logs", logsCommandHandler)
	registerComponentHandler(logsPagePrefix, logsPageHandler)
}

func logsReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	if _, err := database.Pool.Exec(context.Background(), createLogsSQL); err != nil {
		log.Println("Error creating logs tables", err)
	}
}

// Only messages people send in servers are logged, so bots (including this one streaming its replies) and system messages are left out.
func isLoggedMessage(m *discordgo.Message) bool {
	return m.GuildID != "" && m.Author != nil && !m.Author.Bot && m.WebhookID == "" &&
		(m.Type == discordgo.MessageTypeDefault || m.Type == discordgo.MessageTypeReply)
}

func logsMessageCreateHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if !isLoggedMessage(m.Message) {
		return
	}
	if _, err := database.Pool.Exec(context.Background(), trackMessageSQL, m.ID, m.ChannelID, m.Author.ID, m.Content); err != nil {
		log.Println("Error tracking message", err)
	}
}

// Updates that leave the content alone, like removing an attachment or loading a link preview, aren't logged.
// Messages sent before the bot started tracking are saved on their first edit, so later edits and deletions get logged.
func logsMessageUpdateHandler(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if !isLoggedMessage(m.Message) {
		return
	}
	editedAt := time.Now()
	if m.EditedTimestamp != nil {
		editedAt = *m.EditedTimestamp
	}
	if _, err := database.Pool.Exec(context.Background(), logEditSQL, m.ID, m.ChannelID, m.Author.ID, m.Content, editedAt.UnixMilli()); err != nil {
		log.Println("Error logging message edit", err)
	}
}

func logsMessageDeleteHandler(s *discordgo.Session, m *discordgo.MessageDelete) {
	logDeletes(m.GuildID, m.ID)
}

func logsMessageDeleteBulkHandler(s *discordgo.Session, m *discordgo.MessageDeleteBulk) {
	logDeletes(m.GuildID, m.Messages...)
}

func logDeletes(guildID string, messageIDs ...string) {
	if guildID == "" {
		return
	}
	ids := make([]int64, 0, len(messageIDs))
	for _, id := range messageIDs {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			log.Println("Error parsing message ID", err)
			return
		}
		ids = append(ids, n)
	}
	if _, err := database.Pool.Exec(context.Background(), logDeletesSQL, ids, time.Now().UnixMilli()); err != nil {
		log.Println("Error logging message deletion", err)
	}
}

func logsCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	logsRespond(s, i, discordgo.InteractionResponseChannelMessageWithSource, 0, math.MaxInt64)
}

func logsPageHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_, state, _ := strings.Cut(i.MessageComponentData().CustomID, ":")
	pageText, lastText, _ := strings.Cut(state, ":")
	page, pageErr := strconv.Atoi(pageText)
	last, lastErr := strconv.ParseInt(lastText, 10, 64)
	if err := errors.Join(pageErr, lastErr); err != nil {
		log.Println("Error parsing logs page", err)
		return
	}
	logsRespond(s, i, discordgo.InteractionResponseUpdateMessage, page, last)
}

// Pages only show entries up to last, the newest entry when /logs ran, so new edits don't shift the pages someone is flipping through.
func logsRespond(s *discordgo.Session, i *discordgo.InteractionCreate, responseType discordgo.InteractionResponseType, page int, last int64) {
	ctx, cancel := context.WithTimeout(context.Background(), logsTimeout)
	defer cancel()

	response := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "Couldn't load the logs, try again later", Flags: discordgo.MessageFlagsEphemeral},
	}
	if entries, err := channelLogs(ctx, i.ChannelID, last); err != nil {
		log.Println("Error loading logs", err)
	} else {
		response = &discordgo.InteractionResponse{Type: responseType, Data: &discordgo.InteractionResponseData{
			Flags:           discordgo.MessageFlagsIsComponentsV2,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
			Components:      logsComponents(i.GuildID, i.ChannelID, entries, page),
		}}
	}
	if err := s.InteractionRespond(i.Interaction, response); err != nil {
		log.Println("Error responding to interaction", err)
	}
}

func channelLogs(ctx context.Context, channelID string, last int64) ([]messageLog, error) {
	rows, err := database.Pool.Query(ctx, selectLogsSQL, channelID, last)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[messageLog])
}

func logsComponents(guildID, channelID string, entries []messageLog, page int) []discordgo.MessageComponent {
	if len(entries) == 0 {
		return []discordgo.MessageComponent{discordgo.Container{AccentColor: new(logsColor), Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: logsHeading + "\nNo edits or deletions have been logged in this channel yet."},
		}}}
	}
	pages := logsPages(guildID, channelID, entries)
	page = min(max(page, 0), len(pages)-1)

	edits := 0
	for _, e := range entries {
		if e.NewContent != nil {
			edits++
		}
	}
	content := []discordgo.MessageComponent{discordgo.TextDisplay{Content: logsHeading}}
	for _, text := range pages[page] {
		content = append(content, discordgo.TextDisplay{Content: text})
	}
	content = append(content, discordgo.TextDisplay{
		Content: fmt.Sprintf("-# Page %d/%d · %d edited · %d deleted", page+1, len(pages), edits, len(entries)-edits),
	})

	last := entries[0].ID
	return []discordgo.MessageComponent{
		discordgo.Container{AccentColor: new(logsColor), Components: content},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Previous", Style: discordgo.SecondaryButton, CustomID: logsPageID(page-1, last), Disabled: page == 0},
			discordgo.Button{Label: "Next", Style: discordgo.SecondaryButton, CustomID: logsPageID(page+1, last), Disabled: page == len(pages)-1},
		}},
	}
}

func logsPageID(page int, last int64) string {
	return fmt.Sprintf("%s:%d:%d", logsPagePrefix, page, last)
}

// logsPages splits the entries, newest first, into pages that each hold as many entries as one message can.
// Each entry gets its own text display, so markdown left open in one message's content can't spill into the next entry.
func logsPages(guildID, channelID string, entries []messageLog) [][]string {
	textLimit := logsMaxText - textLength(logsHeading) - logsFooterLength
	entryLimit := logsMaxComponents - logsPageComponents
	var pages [][]string
	used := 0
	for _, e := range entries {
		text := e.text(guildID, channelID, textLimit)
		length := textLength(text)
		if len(pages) == 0 || len(pages[len(pages)-1]) == entryLimit || used+length > textLimit {
			pages = append(pages, nil)
			used = 0
		}
		pages[len(pages)-1] = append(pages[len(pages)-1], text)
		used += length
	}
	return pages
}

// text renders the entry in at most limit characters, cutting the content short when it's too long.
func (e messageLog) text(guildID, channelID string, limit int) string {
	if e.NewContent == nil {
		head := fmt.Sprintf("**Deleted** · <@%d> · <t:%d:R>\n", e.UserID, e.Timestamp/1000)
		return head + logQuote(e.OldContent, limit-textLength(head))
	}

	head := fmt.Sprintf("**Edited** · <@%d> · <t:%d:R> · [Jump](https://discord.com/channels/%s/%s/%d)\n-# Before\n",
		e.UserID, e.Timestamp/1000, guildID, channelID, e.MessageID)
	const middle = "\n-# After\n"
	budget := limit - textLength(head) - textLength(middle)
	oldLimit := textLength(logQuote(e.OldContent, budget))
	newLimit := textLength(logQuote(*e.NewContent, budget))
	if oldLimit+newLimit > budget {
		half := budget / 2
		switch {
		case oldLimit <= half:
			newLimit = budget - oldLimit
		case newLimit <= half:
			oldLimit = budget - newLimit
		default:
			oldLimit, newLimit = half, budget-half
		}
	}
	return head + logQuote(e.OldContent, oldLimit) + middle + logQuote(*e.NewContent, newLimit)
}

// logQuote formats content as a block quote of at most limit characters, ending it with an ellipsis if it had to be cut.
func logQuote(content string, limit int) string {
	if content == "" {
		return logsNoText
	}
	quote := func(text string) string { return "> " + strings.ReplaceAll(text, "\n", "\n> ") }
	if quoted := quote(content); textLength(quoted) <= limit {
		return quoted
	}
	length := textLength(quote("…"))
	for n, r := range content {
		length += utf16.RuneLen(r)
		if r == '\n' {
			length += textLength("> ")
		}
		if length > limit {
			return quote(content[:n] + "…")
		}
	}
	return quote(content)
}

// textLength counts UTF-16 code units rather than runes, so a page full of emoji still can't go over Discord's limits.
func textLength(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	return length
}
