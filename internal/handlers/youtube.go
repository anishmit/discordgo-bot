package handlers

import (
	"os/exec"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"encoding/json"
	"github.com/bwmarrin/discordgo"
	"strings"
	"github.com/jonas747/ogg"
	"sync"
)

const MAX_RESULTS = "25"

type SearchResult struct {
	Title string
	ID string
	Duration string
}

type guildQueue struct {
	queue []string
	nowPlaying int
	voiceConnection *discordgo.VoiceConnection
	stopChannel chan bool
	mu sync.Mutex
}

var (
	guildQueues = make(map[string]*guildQueue)
	queueMutex sync.Mutex
)

func init() {
	registerCommandHandler("youtube", youtubeCommandHandler)
	registerComponentHandler("ytSelect", ytSelectHandler)
}

func inVoiceChannel(s *discordgo.Session, guildID, userID string) (bool, string) {
	// An error means that the user isn't in a VC
	if voiceState, err := s.State.VoiceState(guildID, userID); err != nil {
		return false, ""
	} else {
		return true, voiceState.ChannelID
	}
}

func search(query string) ([]SearchResult, error) {
	URL1, err := url.Parse("https://www.googleapis.com/youtube/v3/search")
	if err != nil {
		return nil, err
	}
	parameters1 := url.Values{}
	parameters1.Add("key", os.Getenv("YOUTUBE_API_KEY"))
	parameters1.Add("part", "snippet")
	parameters1.Add("type", "video")
	parameters1.Add("maxResults", MAX_RESULTS)
	parameters1.Add("q", query)
	URL1.RawQuery = parameters1.Encode()
	res1, err := http.Get(URL1.String())

	if err != nil {
		return nil, err
	}
	var data1 map[string]any
	json.NewDecoder(res1.Body).Decode(&data1)
	items1 := data1["items"].([]any)

	URL2, err := url.Parse("https://www.googleapis.com/youtube/v3/videos")
	if err != nil {
		return nil, err
	}
	parameters2 := url.Values{}
	parameters2.Add("key", os.Getenv("YOUTUBE_API_KEY"))
	parameters2.Add("part", "contentDetails")
	parameters2.Add("maxResults", MAX_RESULTS)
	commaSeparatedIDs := ""
	for _, item := range items1 {
		commaSeparatedIDs += item.(map[string]any)["id"].(map[string]any)["videoId"].(string) + ","
	}
	parameters2.Add("id", commaSeparatedIDs)
	URL2.RawQuery = parameters2.Encode()
	res2, err := http.Get(URL2.String())

	if err != nil {
		return nil, err
	}
	var data2 map[string]any
	json.NewDecoder(res2.Body).Decode(&data2)
	items2 := data2["items"].([]any)

	var results []SearchResult
	for i, item1 := range items1 {
		results = append(results, SearchResult{
			Title: item1.(map[string]any)["snippet"].(map[string]any)["title"].(string),
			ID: item1.(map[string]any)["id"].(map[string]any)["videoId"].(string),
			Duration: strings.ToLower(strings.Replace(items2[i].(map[string]any)["contentDetails"].(map[string]any)["duration"].(string)[1:], "T", "", 1)),
		})
	}
	return results, nil
}

func getOrCreateQueue(guildID string) *guildQueue {
	queueMutex.Lock()
	defer queueMutex.Unlock()

	if _, ok := guildQueues[guildID]; !ok {
		guildQueues[guildID] = &guildQueue{
			queue: []string{},
			nowPlaying: -1,
			stopChannel: make(chan bool, 1),
		}
	}
	return guildQueues[guildID]
}

func playVideo(s *discordgo.Session, guildID string, channelID string, videoID string, queue *guildQueue) {
	voice, err := s.ChannelVoiceJoin(guildID, channelID, false, false)
	if err != nil {
		log.Println("Could not join voice channel", err)
		return
	}

	queue.mu.Lock()
	queue.voiceConnection = voice
	queue.mu.Unlock()

	cmd1 := exec.Command("yt-dlp", "-f", "ba", "-o", "-", fmt.Sprintf("https://youtube.com/watch?v=%s", videoID))
	cmd2 := exec.Command("ffmpeg", "-i", "-", "-c:a", "libopus", "-b:a", "96K", "-ar", "48000", "-ac", "2", "-f", "opus", "-")
	cmd2.Stdin, err = cmd1.StdoutPipe()
	if err != nil {
		log.Println("Could not get command 1 standard output pipe", err)
		return
	}
	pipe, err := cmd2.StdoutPipe()
	if err != nil {
		log.Println("Could not get command 2 standard output pipe", err)
		return
	}
	if err = cmd1.Start(); err != nil {
		log.Println("Could not start command 1", err)
		return
	}
	if err = cmd2.Start(); err != nil {
		log.Println("Could not start command 2", err)
		return
	}

	decoder := ogg.NewPacketDecoder(ogg.NewDecoder(pipe))
	voice.Speaking(true)

	playbackDone := make(chan bool)
	go func() {
		for {
			select {
			case <-queue.stopChannel:
				cmd1.Process.Kill()
				cmd2.Process.Kill()
				playbackDone <- true
				return
			default:
				packet, _, err := decoder.Decode()
				if err != nil {
					playbackDone <- true
					return
				}
				select {
				case voice.OpusSend <- packet:
				case <-queue.stopChannel:
					cmd1.Process.Kill()
					cmd2.Process.Kill()
					playbackDone <- true
					return
				}
			}
		}
	}()

	<-playbackDone
	voice.Speaking(false)
	cmd2.Wait()
	cmd1.Wait()

	// Play next video if available
	queue.mu.Lock()
	if queue.nowPlaying < len(queue.queue)-1 {
		queue.nowPlaying++
		nextVideoID := queue.queue[queue.nowPlaying]
		queue.mu.Unlock()
		playVideo(s, guildID, channelID, nextVideoID, queue)
	} else {
		// Queue finished, leave VC
		queue.nowPlaying = -1
		queue.queue = []string{}
		if queue.voiceConnection != nil {
			queue.voiceConnection.Disconnect()
			queue.voiceConnection = nil
		}
		queue.mu.Unlock()
	}
}

func getVideoInfo(videoIDs []string) (map[string]SearchResult, error) {
	if len(videoIDs) == 0 {
		return map[string]SearchResult{}, nil
	}

	URL, err := url.Parse("https://www.googleapis.com/youtube/v3/videos")
	if err != nil {
		return nil, err
	}
	parameters := url.Values{}
	parameters.Add("key", os.Getenv("YOUTUBE_API_KEY"))
	parameters.Add("part", "snippet,contentDetails")
	parameters.Add("id", strings.Join(videoIDs, ","))
	URL.RawQuery = parameters.Encode()
	res, err := http.Get(URL.String())

	if err != nil {
		return nil, err
	}
	var data map[string]any
	json.NewDecoder(res.Body).Decode(&data)
	items := data["items"].([]any)

	results := make(map[string]SearchResult)
	for _, item := range items {
		itemMap := item.(map[string]any)
		videoID := itemMap["id"].(string)
		results[videoID] = SearchResult{
			Title: itemMap["snippet"].(map[string]any)["title"].(string),
			ID: videoID,
			Duration: strings.ToLower(strings.Replace(itemMap["contentDetails"].(map[string]any)["duration"].(string)[1:], "T", "", 1)),
		}
	}
	return results, nil
}

func youtubeCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options

	switch options[0].Name {
	case "play":
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		})

		searchQuery := options[0].Options[0].StringValue()
		searchResults, err := search(searchQuery)
		if err != nil {
			s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
				Content: "Search request failed.",
			})
			return
		} else if len(searchResults) == 0 {
			s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
				Content: fmt.Sprintf("No results found for %s.", searchQuery[:min(1978, len(searchQuery))]),
			})
			return
		}

		var selectMenuOptions []discordgo.SelectMenuOption
		for _, searchResult := range searchResults {
			selectMenuOptions = append(selectMenuOptions, discordgo.SelectMenuOption{
				Label: searchResult.Title[:min(100, len(searchResult.Title))],
				Value: searchResult.ID,
				Description: searchResult.Duration,
			})
		}
		placeholderText := fmt.Sprintf("Results for %s", searchQuery)
		s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							MenuType: discordgo.StringSelectMenu,
							CustomID: "ytSelect",
							Placeholder: placeholderText[:min(150, len(placeholderText))],
							MaxValues: len(selectMenuOptions),
							Options: selectMenuOptions,
						},
					},
				},
			},
		})

	case "next":
		queue := getOrCreateQueue(i.GuildID)
		queue.mu.Lock()
		defer queue.mu.Unlock()

		if queue.nowPlaying == -1 {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Nothing is currently playing.",
				},
			})
			return
		}

		// Send skip signal
		select {
		case queue.stopChannel <- true:
		default:
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Skipped the current video.",
			},
		})

	case "queue":
		queue := getOrCreateQueue(i.GuildID)
		queue.mu.Lock()
		defer queue.mu.Unlock()

		if queue.nowPlaying == -1 || len(queue.queue) == 0 {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "The queue is empty.",
				},
			})
			return
		}

		// Get video info for queue
		videoInfo, err := getVideoInfo(queue.queue)
		if err != nil {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Failed to get queue information.",
				},
			})
			return
		}

		var description string

		// Now playing
		if queue.nowPlaying >= 0 && queue.nowPlaying < len(queue.queue) {
			currentVideo := videoInfo[queue.queue[queue.nowPlaying]]
			description = fmt.Sprintf("**Now playing:**\n%s (%s)\n", currentVideo.Title, currentVideo.Duration)
		}

		// Next up
		if queue.nowPlaying < len(queue.queue)-1 {
			description += "\n**Next up:**\n"
			for i := queue.nowPlaying + 1; i < len(queue.queue); i++ {
				video := videoInfo[queue.queue[i]]
				description += fmt.Sprintf("%d. %s (%s)\n", i-queue.nowPlaying, video.Title, video.Duration)
			}
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{
					{
						Title:       "Queue",
						Description: description,
						Color:       0xff0000,
					},
				},
			},
		})

	case "move":
		if i.Member == nil {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "You must be in a guild voice channel to use this command.",
				},
			})
			return
		}

		inVC, channelID := inVoiceChannel(s, i.GuildID, i.Member.User.ID)
		if !inVC {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "You must be in a voice channel to use this command.",
				},
			})
			return
		}

		queue := getOrCreateQueue(i.GuildID)
		queue.mu.Lock()
		defer queue.mu.Unlock()

		if queue.nowPlaying == -1 {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Nothing is currently playing.",
				},
			})
			return
		}

		// Move to new voice channel
		if queue.voiceConnection != nil {
			queue.voiceConnection.ChangeChannel(channelID, false, false)
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Joined your voice channel.",
			},
		})
	}
}

func ytSelectHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})

	if i.Member == nil {
		content := "You must be in a guild voice channel to use this command."
		s.FollowupMessageEdit(i.Interaction, i.Message.ID, &discordgo.WebhookEdit{
			Content: &content,
		})
		return
	}

	inVC, channelID := inVoiceChannel(s, i.GuildID, i.Member.User.ID)
	if !inVC {
		content := "You must be in a voice channel to use this command."
		s.FollowupMessageEdit(i.Interaction, i.Message.ID, &discordgo.WebhookEdit{
			Content: &content,
		})
		return
	}

	videoIDs := i.MessageComponentData().Values
	queue := getOrCreateQueue(i.GuildID)

	queue.mu.Lock()
	wasEmpty := queue.nowPlaying == -1
	queue.queue = append(queue.queue, videoIDs...)
	queue.mu.Unlock()

	content := fmt.Sprintf("Added %d video(s) to the queue.", len(videoIDs))
	s.FollowupMessageEdit(i.Interaction, i.Message.ID, &discordgo.WebhookEdit{
		Content: &content,
	})

	// If nothing was playing, start playing
	if wasEmpty {
		queue.mu.Lock()
		queue.nowPlaying = 0
		firstVideoID := queue.queue[0]
		queue.mu.Unlock()

		go playVideo(s, i.GuildID, channelID, firstVideoID, queue)
	}
}