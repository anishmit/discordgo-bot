package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/jackc/pgx/v5"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const (
	latexColor          = 0xC27C0E
	latexLeaderboardLen = 15
	latexMatchThreshold = 0.961
	latexRenderTimeout  = 10 * time.Second
)

const latexDocument = `<!DOCTYPE html>
<html>
	<head>
		<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/katex@0.16.22/dist/katex.min.css" integrity="sha384-5TcZemv2l/9On385z///+d7MSYlvIEw9FuZTIdZ14vJLqWphw7e7ZPuOiCHJcFCP" crossorigin="anonymous">
		<script defer src="https://cdn.jsdelivr.net/npm/katex@0.16.22/dist/katex.min.js" integrity="sha384-cMkvdD8LoxVzGF/RPUKAcvmm49FQ0oxwDF3BGKtDXcEc+T1b2N+teh/OJfpU0jr6" crossorigin="anonymous"></script>
		<style>
			.katex { font-size: 2em; }
		</style>
	</head>
	<body><div id="latex" style="display: inline-block"></div></body>
</html>`

type latexProblem struct {
	id          int64
	title       string
	image       image.Image
	createdTime time.Time
}

type latexLeaderboardEntry struct {
	UserID    int64
	Timestamp int64
	WPM       float64
}

var (
	chromedpCtx   context.Context
	latexRenderMu sync.Mutex

	latexProblemsMu sync.Mutex
	latexProblems   = map[string][]latexProblem{}
)

func init() {
	chromedpCtx, _ = chromedp.NewContext(context.Background())
	if err := chromedp.Do(chromedpCtx, chromedp.Navigate("about:blank"), setDocumentContent(latexDocument)); err != nil {
		log.Fatalln("Failed to run chromedp", err)
	}
	registerCommandHandler("latex", latexCommandHandler)
}

func setDocumentContent(html string) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		tree, err := cdp.Call(ctx, t, page.GetFrameTree, cdp.Empty{})
		if err != nil {
			return err
		}
		_, err = cdp.Call(ctx, t, page.SetDocumentContent, page.SetDocumentContentParams{FrameID: tree.FrameTree.Frame.ID, HTML: html})
		return err
	})
}

func latexCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	switch i.ApplicationCommandData().Options[0].Name {
	case "answer":
		latexAnswerCommandHandler(s, i)
	case "problem":
		latexProblemCommandHandler(s, i)
	case "leaderboard":
		latexLeaderboardCommandHandler(s, i)
	}
}

func latexAnswerCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	latex := i.ApplicationCommandData().Options[0].Options[0].StringValue()
	pngData, img, renderTime, err := renderLatex(latex)
	if err != nil {
		followupError(s, i, "Failed to render LaTeX", err)
		return
	}
	followup(s, i, latexImageMessage("", pngData, renderTime))

	answerTime, err := discordgo.SnowflakeTimestamp(i.ID)
	if err != nil {
		log.Println("Error getting interaction time", err)
		return
	}
	userID, err := strconv.ParseInt(interactionUserID(i), 10, 64)
	if err != nil {
		log.Println("Error parsing user id", err)
		return
	}

	for _, problem := range takeSolvedProblems(i.ChannelID, img) {
		timeTaken := answerTime.Sub(problem.createdTime).Seconds()
		var bestSolution string
		err := database.Pool.QueryRow(context.Background(), `
			WITH upd AS (
				UPDATE latex_problems
				SET best_solution = CASE
					WHEN octet_length(best_solution) > octet_length($2::text) THEN $2::text
					ELSE best_solution
				END
				WHERE id = $1::bigint
				RETURNING id, best_solution
			)
			INSERT INTO latex_solutions (problem_id, user_id, timestamp_ms, time_taken, solution)
			SELECT id, $3::bigint, $4::bigint, $5::double precision, $2::text FROM upd
			RETURNING (SELECT best_solution FROM upd)
		`, problem.id, latex, userID, answerTime.UnixMilli(), timeTaken).Scan(&bestSolution)
		if err != nil {
			followupError(s, i, "Error recording solution", err)
			continue
		}
		followup(s, i, latexMessage(discordgo.TextDisplay{
			Content: fmt.Sprintf("# Solved %s\nTime Taken: %.2f seconds\nWPM: %.2f\n```latex\n%s```", problem.title, timeTaken, float64(len(bestSolution))/timeTaken*12, bestSolution),
		}))
	}
}

func latexProblemCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var problem latexProblem
	var solution string
	err := database.Pool.QueryRow(context.Background(), "SELECT id, title, solution FROM latex_problems ORDER BY random() LIMIT 1").Scan(&problem.id, &problem.title, &solution)
	if err != nil {
		followupError(s, i, "Error getting problem", err)
		return
	}
	pngData, img, renderTime, err := renderLatex(solution)
	if err != nil {
		followupError(s, i, "Failed to render LaTeX", err)
		return
	}
	if err := followup(s, i, latexImageMessage("# "+problem.title, pngData, renderTime)); err != nil {
		return
	}
	problem.image = img
	problem.createdTime = time.Now()

	latexProblemsMu.Lock()
	defer latexProblemsMu.Unlock()
	latexProblems[i.ChannelID] = append(latexProblems[i.ChannelID], problem)
}

func latexLeaderboardCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	entries, err := latexLeaderboard()
	if err != nil {
		followupError(s, i, "Error getting latex data", err)
		return
	}
	var content strings.Builder
	content.WriteString("## LaTeX Leaderboard\n")
	for rank, e := range entries {
		fmt.Fprintf(&content, "%d. <@%d>: **%.2f** WPM on <t:%d:d>\n", rank+1, e.UserID, e.WPM, e.Timestamp/1000)
	}
	followup(s, i, latexMessage(discordgo.TextDisplay{Content: content.String()}))
}

func latexLeaderboard() ([]latexLeaderboardEntry, error) {
	rows, err := database.Pool.Query(context.Background(), `
		SELECT s.user_id, s.timestamp_ms, octet_length(p.best_solution)::double precision / s.time_taken * 12 AS wpm
		FROM latex_solutions s
		JOIN latex_problems p ON p.id = s.problem_id
		WHERE s.time_taken > 0
		ORDER BY wpm DESC
		LIMIT $1
	`, latexLeaderboardLen)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[latexLeaderboardEntry])
}

func renderLatex(latex string) ([]byte, image.Image, time.Duration, error) {
	latexJSON, _ := json.Marshal(latex)
	script := fmt.Sprintf(`(async () => {
		katex.render(%s, document.getElementById("latex"), {throwOnError: false, displayMode: true});
		await document.fonts.ready;
	})()`, latexJSON)

	latexRenderMu.Lock()
	defer latexRenderMu.Unlock()
	ctx, cancel := context.WithTimeout(chromedpCtx, latexRenderTimeout)
	defer cancel()

	start := time.Now()
	if err := chromedp.Do(ctx, chromedp.Evaluate[chromedp.Void](script, chromedp.EvalAwaitPromise)); err != nil {
		return nil, nil, 0, err
	}
	pngData, err := chromedp.Run(ctx, chromedp.Screenshot(chromedp.ID("latex")))
	if err != nil {
		return nil, nil, 0, err
	}
	renderTime := time.Since(start)
	img, err := png.Decode(bytes.NewReader(pngData))
	return pngData, img, renderTime, err
}

func latexMessage(components ...discordgo.MessageComponent) *discordgo.WebhookParams {
	return &discordgo.WebhookParams{
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components:      []discordgo.MessageComponent{discordgo.Container{AccentColor: new(latexColor), Components: components}},
	}
}

func latexImageMessage(heading string, pngData []byte, renderTime time.Duration) *discordgo.WebhookParams {
	const file = "image.png"
	var components []discordgo.MessageComponent
	if heading != "" {
		components = append(components, discordgo.TextDisplay{Content: heading})
	}
	components = append(components,
		discordgo.MediaGallery{Items: []discordgo.MediaGalleryItem{{Media: discordgo.UnfurledMediaItem{URL: "attachment://" + file}}}},
		discordgo.TextDisplay{Content: fmt.Sprintf("-# %d ms", renderTime.Milliseconds())},
	)
	params := latexMessage(components...)
	params.Files = []*discordgo.File{{Name: file, ContentType: "image/png", Reader: bytes.NewReader(pngData)}}
	return params
}

func takeSolvedProblems(channel string, answer image.Image) []latexProblem {
	latexProblemsMu.Lock()
	defer latexProblemsMu.Unlock()
	var solved, unsolved []latexProblem
	for _, problem := range latexProblems[channel] {
		if similarImages(problem.image, answer) {
			solved = append(solved, problem)
		} else {
			unsolved = append(unsolved, problem)
		}
	}
	latexProblems[channel] = unsolved
	return solved
}

func similarImages(a, b image.Image) bool {
	aBounds, bBounds := a.Bounds(), b.Bounds()
	if aBounds.Size() != bBounds.Size() {
		return false
	}
	matching := 0
	for y := range aBounds.Dy() {
		for x := range aBounds.Dx() {
			if a.At(aBounds.Min.X+x, aBounds.Min.Y+y) == b.At(bBounds.Min.X+x, bBounds.Min.Y+y) {
				matching++
			}
		}
	}
	return float64(matching) >= latexMatchThreshold*float64(aBounds.Dx()*aBounds.Dy())
}

func followup(s *discordgo.Session, i *discordgo.InteractionCreate, params *discordgo.WebhookParams) error {
	_, err := s.FollowupMessageCreate(i.Interaction, false, params)
	if err != nil {
		log.Println("Error sending followup message", err)
	}
	return err
}

func followupError(s *discordgo.Session, i *discordgo.InteractionCreate, message string, err error) {
	log.Println(message, err)
	followup(s, i, &discordgo.WebhookParams{Content: message})
}
