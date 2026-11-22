package handlers

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	udColor          = 0xff8000
	udNoResultsColor = 0xff0000
	udTimeout        = 2500 * time.Millisecond
	udPagePrefix     = "ud"
	udUnpolyVersion  = "3.14.2"
	maxUDResults     = 10
	maxUDDefinition  = 2500
	maxUDExample     = 1000
)

var udVotesPattern = regexp.MustCompile(`id="vote-buttons-(\d+)"[\s\S]*?aria-label="Upvote \((\d+)\)"[\s\S]*?aria-label="Downvote \((\d+)\)"`)

type udDefinition struct {
	Definition string `json:"definition"`
	Permalink  string `json:"permalink"`
	Author     string `json:"author"`
	Word       string `json:"word"`
	DefID      int64  `json:"defid"`
	WrittenOn  string `json:"written_on"`
	Example    string `json:"example"`
	up         int
	down       int
	hasVotes   bool
}

func init() {
	registerCommandHandler("ud", udCommandHandler)
	registerComponentHandler(udPagePrefix, udPageHandler)
}

func udCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	udRespond(s, i, discordgo.InteractionResponseChannelMessageWithSource, i.ApplicationCommandData().Options[0].StringValue(), 0)
}

func udPageHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	_, state, _ := strings.Cut(i.MessageComponentData().CustomID, ":")
	pageText, term, _ := strings.Cut(state, ":")
	page, err := strconv.Atoi(pageText)
	if err != nil {
		log.Println("Error parsing Urban Dictionary page", err)
		return
	}
	udRespond(s, i, discordgo.InteractionResponseUpdateMessage, term, page)
}

func udRespond(s *discordgo.Session, i *discordgo.InteractionCreate, responseType discordgo.InteractionResponseType, term string, page int) {
	ctx, cancel := context.WithTimeout(context.Background(), udTimeout)
	defer cancel()

	response := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: "Couldn't reach Urban Dictionary, try again later", Flags: discordgo.MessageFlagsEphemeral},
	}
	if defs, err := udDefinitions(ctx, term); err != nil {
		log.Println("Error getting Urban Dictionary definitions", err)
	} else {
		response = &discordgo.InteractionResponse{Type: responseType, Data: &discordgo.InteractionResponseData{
			Flags:           discordgo.MessageFlagsIsComponentsV2,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
			Components:      udComponents(term, defs, page),
		}}
	}
	if err := s.InteractionRespond(i.Interaction, response); err != nil {
		log.Println("Error responding to interaction", err)
	}
}

func udGet(ctx context.Context, url string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	maps.Copy(req.Header, header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("Urban Dictionary returned %s", res.Status)
	}
	return res, nil
}

func udDefinitions(ctx context.Context, term string) ([]udDefinition, error) {
	res, err := udGet(ctx, "https://api.urbandictionary.com/v0/define?term="+url.QueryEscape(term), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var response struct {
		List []udDefinition `json:"list"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, err
	}
	defs := response.List
	addUDVotes(ctx, defs)
	slices.SortStableFunc(defs, func(a, b udDefinition) int {
		if exact := strings.EqualFold(a.Word, term); exact != strings.EqualFold(b.Word, term) {
			if exact {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.up, a.up)
	})
	return defs[:min(len(defs), maxUDResults)], nil
}

func addUDVotes(ctx context.Context, defs []udDefinition) {
	if len(defs) == 0 {
		return
	}
	ids := make([]string, len(defs))
	for i, d := range defs {
		ids[i] = strconv.FormatInt(d.DefID, 10)
	}
	res, err := udGet(ctx, "https://www.urbandictionary.com/ui/votes?defids="+strings.Join(ids, ","), http.Header{"X-Up-Version": {udUnpolyVersion}})
	if err != nil {
		log.Println("Error getting Urban Dictionary votes", err)
		return
	}
	defer res.Body.Close()
	page, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		log.Println("Error reading Urban Dictionary votes", err)
		return
	}

	votes := map[string][2]int{}
	for _, match := range udVotesPattern.FindAllStringSubmatch(string(page), -1) {
		up, _ := strconv.Atoi(match[2])
		down, _ := strconv.Atoi(match[3])
		votes[match[1]] = [2]int{up, down}
	}
	for i := range defs {
		if v, ok := votes[ids[i]]; ok {
			defs[i].up, defs[i].down, defs[i].hasVotes = v[0], v[1], true
		}
	}
}

func udComponents(term string, defs []udDefinition, page int) []discordgo.MessageComponent {
	if len(defs) == 0 {
		return []discordgo.MessageComponent{discordgo.Container{AccentColor: new(udNoResultsColor), Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: "No results were found for " + term},
		}}}
	}
	page = min(max(page, 0), len(defs)-1)
	d := defs[page]

	details := []string{"by " + d.Author}
	if t, err := time.Parse(time.RFC3339, d.WrittenOn); err == nil {
		details = append(details, fmt.Sprintf("<t:%d:d>", t.Unix()))
	}
	if d.hasVotes {
		details = append(details, fmt.Sprintf("👍 %d 👎 %d", d.up, d.down))
	}
	details = append(details, fmt.Sprintf("%d/%d", page+1, len(defs)))

	content := []discordgo.MessageComponent{
		discordgo.TextDisplay{Content: fmt.Sprintf("## [%s](%s)\n%s", d.Word, d.Permalink, udExcerpt(d.Definition, maxUDDefinition))},
	}
	if d.Example != "" {
		content = append(content, discordgo.TextDisplay{Content: "**Example**\n" + udExcerpt(d.Example, maxUDExample)})
	}
	content = append(content, discordgo.TextDisplay{Content: "-# " + strings.Join(details, " • ")})

	return []discordgo.MessageComponent{
		discordgo.Container{AccentColor: new(udColor), Components: content},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Previous", Style: discordgo.SecondaryButton, CustomID: udPageID(page-1, term), Disabled: page == 0},
			discordgo.Button{Label: "Next", Style: discordgo.SecondaryButton, CustomID: udPageID(page+1, term), Disabled: page == len(defs)-1},
		}},
	}
}

func udPageID(page int, term string) string {
	return fmt.Sprintf("%s:%d:%s", udPagePrefix, page, term)
}

func udExcerpt(text string, limit int) string {
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-3]) + "..."
	}
	return text
}
