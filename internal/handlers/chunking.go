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

	"github.com/bwmarrin/discordgo"
	"github.com/jackc/pgx/v5"
	"google.golang.org/genai"

	"github.com/anishmit/discordgo-bot/internal/clients"
	"github.com/anishmit/discordgo-bot/internal/database"
)

const (
	chunkSyncInterval = 5 * time.Minute

	gapBreak = 30 * time.Minute
	maxChars = 2048 * 3

	embedModel    = "gemini-embedding-001"
	embedDims     = 768
	embedTaskType = "RETRIEVAL_DOCUMENT"
)

func init() {
	registerReadyHandler(chunkReadyHandler)
}

func chunkReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	go runChunkSync()
}

func runChunkSync() {
	ctx := context.Background()
	for {
		if err := updateChunks(ctx); err != nil {
			log.Println("Error updating chunks", err)
		}
		time.Sleep(chunkSyncInterval)
	}
}

type chunkMessage struct {
	userID  int64
	tsMS    int64
	content string
}

type chunk struct {
	startMS int64
	endMS   int64
	userIDs []int64
	content string
}

func formatLine(m chunkMessage) string {
	t := time.UnixMilli(m.tsMS).UTC().Format("2006-01-02 15:04")
	content := strings.ReplaceAll(m.content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	return fmt.Sprintf("[%s] <@%d>: %s", t, m.userID, content)
}

type chunker struct {
	msgs    []chunkMessage
	users   []int64
	userSet map[int64]bool
	text    strings.Builder
	lastTS  int64
}

func (c *chunker) add(m chunkMessage) *chunk {
	var out *chunk
	if len(c.msgs) > 0 {
		gap := time.Duration(m.tsMS - c.lastTS) * time.Millisecond
		if gap > gapBreak || c.wouldExceed(m) {
			out = c.finish()
		}
	}
	c.append(m)
	c.lastTS = m.tsMS
	return out
}

func (c *chunker) flush() *chunk {
	if len(c.msgs) == 0 {
		return nil
	}
	return c.finish()
}

func (c *chunker) wouldExceed(m chunkMessage) bool {
	add := len(formatLine(m))
	if c.text.Len() > 0 {
		add++
	}
	return c.text.Len() + add > maxChars
}

func (c *chunker) append(m chunkMessage) {
	if c.text.Len() > 0 {
		c.text.WriteByte('\n')
	}
	c.text.WriteString(formatLine(m))
	c.msgs = append(c.msgs, m)
	if c.userSet == nil {
		c.userSet = map[int64]bool{}
	}
	if !c.userSet[m.userID] {
		c.userSet[m.userID] = true
		c.users = append(c.users, m.userID)
	}
}

func (c *chunker) finish() *chunk {
	out := &chunk{
		startMS: c.msgs[0].tsMS,
		endMS:   c.msgs[len(c.msgs) - 1].tsMS,
		userIDs: c.users,
		content: c.text.String(),
	}
	c.msgs = nil
	c.users = nil
	c.userSet = nil
	c.text.Reset()
	return out
}

func updateChunks(ctx context.Context) error {
	// Find the last chunk.
	var rechunkFrom int64
	var lastChunkID *int64
	err := database.Pool.QueryRow(ctx, `SELECT id, start_ms FROM message_chunks ORDER BY end_ms DESC LIMIT 1`).Scan(&lastChunkID, &rechunkFrom)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			rechunkFrom = 0
		} else {
			return err
		}
	}

	// Stop if no messages arrived past the last chunk's end.
	var newCount int64
	if err := database.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM messages
		WHERE content <> '' AND timestamp_ms > (SELECT COALESCE(MAX(end_ms), -1) FROM message_chunks)
	`).Scan(&newCount); err != nil {
		return err
	}
	if newCount == 0 {
		return nil
	}

	// Delete the last chunk so it can be rebuilt with the new messages.
	if lastChunkID != nil {
		if _, err := database.Pool.Exec(ctx, `DELETE FROM message_chunks WHERE id = $1`, *lastChunkID); err != nil {
			return err
		}
	}

	// Get the dropped chunk's messages plus the new ones.
	rows, err := database.Pool.Query(ctx, `
		SELECT user_id, timestamp_ms, content
		FROM messages
		WHERE content <> '' AND timestamp_ms >= $1
		ORDER BY timestamp_ms, message_id
	`, rechunkFrom)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Feed them through the chunker, collecting completed chunks.
	var ch chunker
	var pending []*chunk
	for rows.Next() {
		var m chunkMessage
		if err := rows.Scan(&m.userID, &m.tsMS, &m.content); err != nil {
			return err
		}
		if done := ch.add(m); done != nil {
			pending = append(pending, done)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if last := ch.flush(); last != nil {
		pending = append(pending, last)
	}

	// Embed and store each rebuilt chunk.
	for _, c := range pending {
		if err := embedAndStoreChunk(ctx, c); err != nil {
			return err
		}
	}

	if len(pending) > 0 {
		log.Printf("Embedded %d chunk(s)", len(pending))
	}
	return nil
}

func embedAndStoreChunk(ctx context.Context, c *chunk) error {
	dim := int32(embedDims)
	res, err := clients.GeminiClient.Models.EmbedContent(ctx, embedModel,
		[]*genai.Content{genai.NewContentFromText(c.content, genai.RoleUser)},
		&genai.EmbedContentConfig{TaskType: embedTaskType, OutputDimensionality: &dim})
	if err != nil {
		return err
	}
	vec := normalize(res.Embeddings[0].Values)
	_, err = database.Pool.Exec(ctx, `
		INSERT INTO message_chunks (start_ms, end_ms, user_ids, content, embedding)
		VALUES ($1, $2, $3, $4, $5::halfvec)
	`, c.startMS, c.endMS, c.userIDs, c.content, vectorString(vec))
	return err
}

func normalize(vals []float32) []float32 {
	var sum float64
	for _, v := range vals {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return vals
	}
	norm := float32(1.0 / math.Sqrt(sum))
	out := make([]float32, len(vals))
	for i, v := range vals {
		out[i] = v * norm
	}
	return out
}

func vectorString(vals []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range vals {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
