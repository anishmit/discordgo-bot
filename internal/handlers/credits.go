package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/anishmit/discordgo-bot/internal/google"
)

const (
	creditsColor   = 0x34A853
	creditsTimeout = 45 * time.Second
	maxCredits     = 10
)

type credit struct {
	Name      string    `json:"displayName"`
	Value     money     `json:"value"`
	Remaining money     `json:"remainingAmount"`
	End       time.Time `json:"endTime"`
}

type money struct {
	Units string `json:"units"`
	Nanos int64  `json:"nanos"`
}

func (m money) amount() float64 {
	units, _ := strconv.ParseFloat(m.Units, 64)
	return units + float64(m.Nanos)/1e9
}

func dollars(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

func init() {
	registerCommandHandler("credits", creditsCommandHandler)
}

func creditsCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	account := strings.TrimPrefix(os.Getenv("GCP_BILLING_ACCOUNT"), "billingAccounts/")
	if account == "" {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Set `GCP_BILLING_ACCOUNT` in the bot's environment to use /credits.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Println("Error responding to interaction", err)
		}
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println("Error deferring interaction", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), creditsTimeout)
	defer cancel()
	credits, err := fetchCredits(ctx, account)
	if err != nil {
		log.Println("Error fetching GCP credits", err)
		followup(s, i, &discordgo.WebhookParams{Content: creditsErrorText(err)})
		return
	}
	followup(s, i, &discordgo.WebhookParams{
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components: []discordgo.MessageComponent{discordgo.Container{
			AccentColor: new(creditsColor),
			Components:  creditsComponents(credits, time.Now()),
		}},
	})
}

func fetchCredits(ctx context.Context, account string) ([]credit, error) {
	var credits []credit
	err := google.Watch(ctx, "https://console.cloud.google.com/billing/"+url.PathEscape(account)+"/credits/all", func(body []byte) bool {
		var found bool
		credits, found = parseCredits(body)
		return found
	})
	return credits, err
}

func parseCredits(body []byte) ([]credit, bool) {
	var batches []struct {
		Results []struct {
			Data struct {
				CreditsQuery struct {
					ListCredits *struct {
						Data []credit `json:"data"`
					} `json:"listCredits"`
				} `json:"creditsQuery"`
			} `json:"data"`
		} `json:"results"`
	}
	if json.Unmarshal(body, &batches) != nil {
		return nil, false
	}
	for _, batch := range batches {
		for _, result := range batch.Results {
			if list := result.Data.CreditsQuery.ListCredits; list != nil {
				return list.Data, true
			}
		}
	}
	return nil, false
}

func creditsErrorText(err error) string {
	switch {
	case errors.Is(err, google.ErrSignedOut):
		return "The bot isn't signed in to Google. Sign in from the bot's terminal, then try again."
	case errors.Is(err, context.DeadlineExceeded):
		return "The Cloud Console didn't send any credits in time. Check that `GCP_BILLING_ACCOUNT` is right and that the bot's Google account can view that billing account."
	default:
		return "Couldn't load credits from the Cloud Console. Try again later."
	}
}

func creditsComponents(credits []credit, now time.Time) []discordgo.MessageComponent {
	if len(credits) == 0 {
		return []discordgo.MessageComponent{discordgo.TextDisplay{Content: "## GCP Credits\nThis billing account has no credits."}}
	}

	var active, ended []credit
	for _, c := range credits {
		if c.End.IsZero() || c.End.After(now) {
			active = append(active, c)
		} else {
			ended = append(ended, c)
		}
	}
	slices.SortFunc(active, func(a, b credit) int { return a.End.Compare(b.End) })
	slices.SortFunc(ended, func(a, b credit) int { return b.End.Compare(a.End) })
	credits = append(active, ended...)

	components := []discordgo.MessageComponent{discordgo.TextDisplay{Content: "## GCP Credits"}}
	for _, c := range credits[:min(len(credits), maxCredits)] {
		var expiry string
		switch {
		case c.End.IsZero():
			expiry = "Doesn't expire"
		case c.End.After(now):
			expiry = fmt.Sprintf("Expires <t:%d:D> (<t:%d:R>)", c.End.Unix(), c.End.Unix())
		default:
			expiry = fmt.Sprintf("Expired <t:%d:D>", c.End.Unix())
		}
		components = append(components, discordgo.Separator{}, discordgo.TextDisplay{
			Content: fmt.Sprintf("**%s**\n%s of %s left\n%s", c.Name, dollars(c.Remaining.amount()), dollars(c.Value.amount()), expiry),
		})
	}
	if more := len(credits) - maxCredits; more > 0 {
		components = append(components, discordgo.Separator{}, discordgo.TextDisplay{Content: fmt.Sprintf("-# …and %d more", more)})
	}
	return components
}
