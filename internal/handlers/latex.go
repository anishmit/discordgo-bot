package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"log"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/anishmit/discordgo-bot/internal/database"
)

type latexProblem struct {
	title string
	id int64
	image image.Image
	createdTime time.Time
}

type latexLeaderboardEntry struct {
	userID int64
	timestamp int64
	wpm float64
}

const leaderboardLen = 15
var chromedpCtx context.Context
var latexProblems = map[string][]latexProblem{}
var answersQueue []*discordgo.InteractionCreate

func init() {
	migrateLatexData()
	chromedpCtx, _ = chromedp.NewContext(context.Background())
	err := chromedp.Run(
		chromedpCtx,
		chromedp.Navigate("about:blank"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			frameTree, err := page.GetFrameTree().Do(ctx)
			if err != nil {
				return err
			}
			return page.SetDocumentContent(frameTree.Frame.ID,
				`<!DOCTYPE html>
				<html>
					<head>
						<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/katex@0.16.22/dist/katex.min.css" integrity="sha384-5TcZemv2l/9On385z///+d7MSYlvIEw9FuZTIdZ14vJLqWphw7e7ZPuOiCHJcFCP" crossorigin="anonymous">
						<script defer src="https://cdn.jsdelivr.net/npm/katex@0.16.22/dist/katex.min.js" integrity="sha384-cMkvdD8LoxVzGF/RPUKAcvmm49FQ0oxwDF3BGKtDXcEc+T1b2N+teh/OJfpU0jr6" crossorigin="anonymous"></script>
						<style>
							.katex { font-size: 2em; }
						</style>
					</head>
				</html>`).Do(ctx)
		}),
	)
	if err != nil {
		log.Fatalln("Failed to run chromedp", err)
	}
	registerCommandHandler("latex", latexCommandHandler)
}

func latexCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	subcommandName := i.ApplicationCommandData().Options[0].Name
	if subcommandName == "answer" {
		answersQueue = append(answersQueue, i)
		if len(answersQueue) == 1 {
			latexAnswerCommandHandler(s)
		}
	} else if subcommandName == "problem" {
		latexProblemCommandHandler(s, i)
	} else if subcommandName == "leaderboard" {
		latexLeaderboardCommandHandler(s, i)
	}
}

func latexAnswerCommandHandler(s *discordgo.Session) {
	i := answersQueue[0]
	latex := i.ApplicationCommandData().Options[0].Options[0].StringValue()
	picBuf, renderTime, err := renderLatex(latex, i.ID)
	if err != nil {
		log.Println("Failed to render LaTeX", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Failed to render LaTeX",
		})
		return
	}
	s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Content: fmt.Sprintf("-# %d ms", renderTime),
		Files: []*discordgo.File{
			{
				Name:        "image.png",
				ContentType: "image/png",
				Reader:      bytes.NewReader(picBuf),
			},
		},
	})
	img, _, err := image.Decode(bytes.NewReader(picBuf))
	if err != nil {
		log.Println("Failed to decode image", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Failed to decode image",
		})
		return
	}
	ctx := context.Background()
	newProbs := latexProblems[i.ChannelID][:0]
	iTime, err := discordgo.SnowflakeTimestamp(i.Interaction.ID)
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	} else {
		return
	}
	if err != nil {
		log.Println("Error getting interaction time", err)
		return
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		log.Println("Error parsing user id", err)
		return
	}
	for _, problem := range latexProblems[i.ChannelID] {
		if equalImages(problem.image, img) {
			timeTaken := iTime.Sub(problem.createdTime).Seconds()
			var bestSolution string
			err := database.Pool.QueryRow(ctx, `
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
				RETURNING (SELECT best_solution FROM upd);
			`, problem.id, latex, userIDInt, iTime.UnixMilli(), timeTaken).Scan(&bestSolution)
			if err != nil {
				log.Println("Error recording solution", err)
				s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
					Content: "Error recording solution",
				})
				continue
			}
			s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
				Content: fmt.Sprintf("# Solved %s\nTime Taken: %.2f seconds\nWPM: %.2f\n```latex\n%s```", problem.title, timeTaken, float64(len(bestSolution)) / timeTaken * 12, bestSolution),
			})
		} else {
			newProbs = append(newProbs, problem)
		}
	}
	latexProblems[i.ChannelID] = newProbs
	answersQueue = answersQueue[1:]
	if len(answersQueue) > 0 {
		latexAnswerCommandHandler(s)
	}
}

func latexProblemCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	// Choose random problem
	var problemID int64
	var problemTitle, problemSolution string
	err := database.Pool.QueryRow(ctx, `
		SELECT id, title, solution FROM latex_problems ORDER BY random() LIMIT 1;
	`).Scan(&problemID, &problemTitle, &problemSolution)
	if err != nil {
		log.Println("Error getting problem", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Error getting problem",
		})
		return
	}
	// Render latex
	picBuf, renderTime, err := renderLatex(problemSolution, i.ID)
	if err != nil {
		log.Println("Failed to render LaTeX", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Failed to render LaTeX",
		})
		return
	}
	// Decode screenshot
	img, _, err := image.Decode(bytes.NewReader(picBuf))
	if err != nil {
		log.Println("Failed to decode image", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Failed to decode image",
		})
		return
	}
	// Send problem
	_, err = s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Content: fmt.Sprintf("-# %d ms\n# %s", renderTime, problemTitle),
		Files: []*discordgo.File{
			{
				Name:        "image.png",
				ContentType: "image/png",
				Reader:      bytes.NewReader(picBuf),
			},
		},
	})
	if err != nil {
		log.Println("Failed to send message", err)
		return
	}
	// Add problem to channel slice
	latexProblems[i.ChannelID] = append(latexProblems[i.ChannelID], latexProblem{
		title: problemTitle,
		id: problemID,
		image: img,
		createdTime: time.Now(),
	})
}

func latexLeaderboardCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	rows, err := database.Pool.Query(ctx, `
		SELECT s.user_id, s.timestamp_ms, octet_length(p.best_solution)::double precision / s.time_taken * 12 AS wpm
		FROM latex_solutions s
		JOIN latex_problems p ON p.id = s.problem_id
		WHERE s.time_taken > 0
		ORDER BY wpm DESC
		LIMIT $1;
	`, leaderboardLen)
	if err != nil {
		log.Println("Error getting latex data", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Error getting latex data",
		})
		return
	}
	defer rows.Close()
	var entries []latexLeaderboardEntry
	for rows.Next() {
		var entry latexLeaderboardEntry
		if err := rows.Scan(&entry.userID, &entry.timestamp, &entry.wpm); err != nil {
			log.Println("Error scanning row", err)
			continue
		}
		entries = append(entries, entry)
	}
	var description string
	for i, entry := range entries {
		description += fmt.Sprintf(
			"%d. <@%d>: **%.2f** WPM on <t:%d:d>\n",
			i + 1,
			entry.userID,
			entry.wpm,
			entry.timestamp / 1000,
		)
	}
	s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{
			{
				Title: "LaTeX Leaderboard",
				Color: 0xC27C0E,
				Description: description,
			},
		},
	})
}

func renderLatex(latex string, id string) ([]byte, int64, error) {
	startTime := time.Now()
	var picBuf []byte
	err := chromedp.Run(
		chromedpCtx,
		chromedp.Evaluate(
			fmt.Sprintf(`(async() =>
				{elem = document.body.appendChild(document.createElement("div"));
				elem.style.display = "inline-block";
				elem.id = %q;
				katex.render(%q, elem, {throwOnError: false, displayMode: true});
				await document.fonts.ready;
			})();`, id, latex),
			nil,
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			},
		),
		chromedp.Screenshot(fmt.Sprintf(`[id="%s"]`, id), &picBuf),
		chromedp.Evaluate(fmt.Sprintf("document.getElementById(%q).remove();", id), nil),
	)
	return picBuf, time.Since(startTime).Milliseconds(),err
}

func equalImages(img1, img2 image.Image) bool {
	i1Bounds := img1.Bounds()
	i2Bounds := img2.Bounds()
	if i1Bounds.Dx() != i2Bounds.Dx() || i1Bounds.Dy() != i2Bounds.Dy() {
		return false
	}
	equalPixels := 0
	for x := range i1Bounds.Dx() {
		for y := range i1Bounds.Dy() {
			if img1.At(x + i1Bounds.Min.X, y + i1Bounds.Min.Y) == img2.At(x + i2Bounds.Min.X, y + i2Bounds.Min.Y) {
				equalPixels++
			}
		}
	}
	return float64(equalPixels) >= 0.961 * float64(i1Bounds.Dx() * i1Bounds.Dy())
}

// ONE-TIME FIREBASE -> POSTGRES MIGRATION. REMOVE AFTER RUNNING ONCE.

const latexExportPath = "latex-export.json"

type latexExportSolution struct {
	TimeTaken float64 `json:"timeTaken"`
	Timestamp int64 `json:"timestamp"`
	Solution string `json:"solution"`
	UserID string `json:"userID"`
}

type latexExportProblem struct {
	Title string `json:"title"`
	Solution string `json:"solution"`
	BestSolution string `json:"bestSolution"`
	Solutions map[string]latexExportSolution `json:"solutions"`
}

type latexExport struct {
	LatexData map[string]latexExportProblem `json:"latexData"`
}

func migrateLatexData() {
	ctx := context.Background()

	data, err := os.ReadFile(latexExportPath)
	if err != nil {
		log.Fatalln("Migration: error reading export", err)
	}
	var export latexExport
	if err := json.Unmarshal(data, &export); err != nil {
		log.Fatalln("Migration: error parsing export", err)
	}
	if len(export.LatexData) == 0 {
		log.Fatalln("Migration: export contains no latexData")
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		log.Fatalln("Migration: error starting transaction", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS latex_problems (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			title TEXT NOT NULL,
			solution TEXT NOT NULL,
			best_solution TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS latex_solutions (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			problem_id BIGINT NOT NULL REFERENCES latex_problems (id) ON DELETE CASCADE,
			user_id BIGINT NOT NULL,
			timestamp_ms BIGINT NOT NULL,
			time_taken DOUBLE PRECISION NOT NULL,
			solution TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS latex_solutions_problem_id_idx ON latex_solutions (problem_id);
	`); err != nil {
		log.Fatalln("Migration: error creating tables", err)
	}

	var existingProblems int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM latex_problems`).Scan(&existingProblems); err != nil {
		log.Fatalln("Migration: error counting problems", err)
	}
	if existingProblems > 0 {
		log.Fatalf("Migration: latex_problems already has %d rows, refusing to run\n", existingProblems)
	}

	problemKeys := make([]string, 0, len(export.LatexData))
	for problemKey := range export.LatexData {
		problemKeys = append(problemKeys, problemKey)
	}
	sort.Strings(problemKeys)

	numProblems, numSolutions := 0, 0
	for _, problemKey := range problemKeys {
		problem := export.LatexData[problemKey]
		var problemID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO latex_problems (title, solution, best_solution)
			VALUES ($1, $2, $3)
			RETURNING id;
		`, problem.Title, problem.Solution, problem.BestSolution).Scan(&problemID); err != nil {
			log.Fatalln("Migration: error inserting problem", problemKey, err)
		}
		numProblems++
		solutionKeys := make([]string, 0, len(problem.Solutions))
		for solutionKey := range problem.Solutions {
			solutionKeys = append(solutionKeys, solutionKey)
		}
		sort.Strings(solutionKeys)
		for _, solutionKey := range solutionKeys {
			solution := problem.Solutions[solutionKey]
			userID, err := strconv.ParseInt(solution.UserID, 10, 64)
			if err != nil {
				log.Fatalln("Migration: error parsing user id", solutionKey, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO latex_solutions (problem_id, user_id, timestamp_ms, time_taken, solution)
				VALUES ($1, $2, $3, $4, $5);
			`, problemID, userID, solution.Timestamp, solution.TimeTaken, solution.Solution); err != nil {
				log.Fatalln("Migration: error inserting solution", solutionKey, err)
			}
			numSolutions++
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Fatalln("Migration: error committing transaction", err)
	}
	log.Printf("Migration: inserted %d problems and %d solutions\n", numProblems, numSolutions)
}
