package handlers

import (
	"bytes"
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"log"
	"image"
	_ "image/png"
	"time"
	"github.com/anishmit/discordgo-bot/internal/clients"
	"math/rand"
	"firebase.google.com/go/v4/db"
	"sort"
)

type latexProblem struct {
	title string
	id string
	image image.Image
	createdTime time.Time
}

type latexDataSolution struct {
	TimeTaken float64 `json:"timeTaken"`
	Timestamp int64 `json:"timestamp"`
	Solution string `json:"solution"`
	UserID string `json:"userID"`
}

type latexDataProblem struct {
	Title string `json:"title"`
	Solution string `json:"solution"`
	BestSolution string `json:"bestSolution"`
	Solutions map[string]latexDataSolution `json:"solutions"`
}

type latexLeaderboardEntry struct {
	userID string
	timestamp int64
	wpm float64
}

const leaderboardLen = 15
var chromedpCtx context.Context
var latexProblems = map[string][]latexProblem{}
var answersQueue []*discordgo.InteractionCreate

func init() {
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
	ref := clients.FirebaseDBClient.NewRef("latexData")
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
	for _, problem := range latexProblems[i.ChannelID] {
		if equalImages(problem.image, img) {
			problemRef := ref.Child(problem.id)
			var bestSolution string
			problemRef.Child("bestSolution").Transaction(ctx, func(value db.TransactionNode) (any, error) {
				value.Unmarshal(&bestSolution)
				if len(bestSolution) > len(latex) {
					bestSolution = latex
					return latex, nil
				} else {
					return bestSolution, nil
				}
			})
			timeTaken := iTime.Sub(problem.createdTime).Seconds()
			problemRef.Child("solutions").Push(ctx, latexDataSolution{
				TimeTaken: timeTaken,
				Timestamp: iTime.UnixMilli(),
				Solution: latex,
				UserID: userID,
			})
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
	// Get problem IDs
	var latexDataProblemIDs map[string]bool
	ref := clients.FirebaseDBClient.NewRef("latexData")
	err := ref.GetShallow(ctx, &latexDataProblemIDs)
	if err != nil {
		log.Println("Error getting problem IDs", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Error getting problem IDs",
		})
		return
	}
	ids := make([]string, 0, len(latexDataProblemIDs))
	for id := range latexDataProblemIDs {
		ids = append(ids, id)
	}
	// Choose random problem
	problemID := ids[rand.Intn(len(ids))]
	var problem latexDataProblem
	err = ref.Child(problemID).Get(ctx, &problem)
	if err != nil {
		log.Println("Error getting problem", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Error getting problem",
		})
		return
	}
	// Render latex
	picBuf, renderTime, err := renderLatex(problem.Solution, i.ID)
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
		Content: fmt.Sprintf("-# %d ms\n# %s", renderTime, problem.Title),
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
		title: problem.Title,
		id: problemID,
		image: img,
		createdTime: time.Now(),
	})
}

func latexLeaderboardCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	ref := clients.FirebaseDBClient.NewRef("latexData")
	var latexData map[string]latexDataProblem
	err := ref.Get(ctx, &latexData)
	if err != nil {
		log.Println("Error getting latex data", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Error getting latex data",
		})
		return
	}
	var entries []latexLeaderboardEntry
	for _, problem := range latexData {
		bestSolLen := len(problem.BestSolution)
		for _, solution := range problem.Solutions {
			entries = append(entries, latexLeaderboardEntry{
				userID: solution.UserID,
				timestamp: solution.Timestamp,
				wpm: float64(bestSolLen) / solution.TimeTaken * 12,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].wpm > entries[j].wpm
	})
	var description string
	for i := range min(leaderboardLen, len(entries)) {
		description += fmt.Sprintf(
			"%d. <@%s>: **%.2f** WPM on <t:%d:d>\n",
			i + 1,
			entries[i].userID,
			entries[i].wpm,
			entries[i].timestamp / 1000,
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