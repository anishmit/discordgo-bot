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
	"time"
)

var chromedpCtx context.Context

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
	startTime := time.Now()
	latex := i.ApplicationCommandData().Options[0].StringValue()
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
			})();`, i.ID, latex), 
			nil, 
			func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			},
		),
		chromedp.Screenshot(fmt.Sprintf(`[id="%s"]`, i.ID), &picBuf),
		chromedp.Evaluate(fmt.Sprintf("document.getElementById(%q).remove();", i.ID), nil),
	)
	if err != nil {
		log.Println("Failed to render LaTeX", err)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Content: "Failed to render LaTeX",
		})
		return
	}
	s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Content: fmt.Sprintf("-# %d ms", time.Since(startTime).Milliseconds()),
		Files: []*discordgo.File{
			{
				Name:        "image.png",
				ContentType: "image/png",
				Reader:      bytes.NewReader(picBuf),
			},
		},
	})
}
