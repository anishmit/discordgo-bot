package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	maxEmbedTitle     = 256
	maxEmbedField     = 1024
	udColor           = 0xff8000
	udNoResultsColor  = 0xff0000
	udWrittenOnLayout = "2006-01-02T15:04:05.000Z"
)

type udDefinition struct {
	Definition  string `json:"definition"`
	Permalink   string `json:"permalink"`
	ThumbsUp    int    `json:"thumbs_up"`
	Author      string `json:"author"`
	Word        string `json:"word"`
	DefID       int64  `json:"defid"`
	CurrentVote string `json:"current_vote"`
	WrittenOn   string `json:"written_on"`
	Example     string `json:"example"`
	ThumbsDown  int64  `json:"thumbs_down"`
}

type udResponse struct {
	List []udDefinition `json:"list"`
}

func init() {
	registerCommandHandler("ud", udCommandHandler)
}

func getUDResponse(term string) (udResponse, error) {
	resp, err := http.Get("https://api.urbandictionary.com/v0/define?term=" + url.QueryEscape(term))
	if err != nil {
		return udResponse{}, err
	}
	defer resp.Body.Close()

	var response udResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return udResponse{}, err
	}

	list := response.List
	sort.Slice(list, func(i, j int) bool {
		equalI := strings.EqualFold(list[i].Word, term)
		equalJ := strings.EqualFold(list[j].Word, term)
		if equalI != equalJ {
			return equalI
		}
		return list[i].ThumbsUp > list[j].ThumbsUp
	})
	return response, nil
}

func udText(s string, maxLen int) string {
	if s == "" {
		return emptyLabel
	}
	return s[:min(len(s), maxLen)]
}

func udCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	term := i.ApplicationCommandData().Options[0].StringValue()
	response, err := getUDResponse(term)
	if err != nil {
		log.Println("Getting Urban Dictionary response failed", err)
		return
	}

	embed := &discordgo.MessageEmbed{Title: udText(term, maxEmbedTitle)}
	if len(response.List) == 0 {
		embed.Color = udNoResultsColor
		embed.Description = "No results were found."
	} else {
		definition := response.List[0]
		t, err := time.Parse(udWrittenOnLayout, definition.WrittenOn)
		if err != nil {
			log.Println("Failed at parsing date", err)
			return
		}
		embed.Color = udColor
		embed.Fields = []*discordgo.MessageEmbedField{
			{Name: "Definition", Value: udText(definition.Definition, maxEmbedField)},
			{Name: "Example", Value: udText(definition.Example, maxEmbedField)},
			{Name: "Author", Value: udText(definition.Author, maxEmbedField), Inline: true},
			{Name: "Date", Value: fmt.Sprintf("<t:%d:d>", t.Unix()), Inline: true},
			{Name: emptyLabel, Value: fmt.Sprintf("👍 %d\u00A0\u00A0\u00A0\u00A0👎 %d", definition.ThumbsUp, definition.ThumbsDown), Inline: true},
		}
	}

	s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
		Embeds: []*discordgo.MessageEmbed{embed},
	})
}
