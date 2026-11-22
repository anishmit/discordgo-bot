package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/jonas747/ogg"

	"github.com/anishmit/discordgo-bot/internal/voice"
)

const (
	ytColor          = 0xff0000
	ytSelectID       = "ytSelect"
	ytMaxResults     = 25
	ytListLength     = 10
	ytAPITimeout     = 10 * time.Second
	ytJoinTimeout    = 20 * time.Second
	ytMoveTimeout    = 15 * time.Second
	ytSilenceTimeout = time.Second
	ytWaitDelay      = 5 * time.Second
	ytAttempts       = 3
	ytBufferedFrames = int(15 * time.Minute / voice.FrameDuration)
	ytDefaultBitrate = 64000
	ytMaxStderr      = 4096
	ytFormat         = "ba/b[height<=480]/b"
)

var (
	ytIDPattern = regexp.MustCompile(`^[\w-]{11}$`)
	ytEscaper   = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `_`, `\_`, "`", "\\`", `~`, `\~`, `|`, `\|`, `[`, `\[`, `]`, `\]`)

	ytPlayersMu sync.Mutex
	ytPlayers   = map[string]*ytPlayer{}
)

type ytVideo struct {
	id       string
	title    string
	channel  string
	duration time.Duration
	live     bool
}

type ytTrack struct {
	ytVideo
	requester     string
	textChannelID string
}

type ytStream struct {
	frames  chan []byte
	fetched chan struct{}
	cancel  context.CancelFunc
	err     error
}

type ytPlayer struct {
	guildID string
	played  atomic.Int64

	mu        sync.Mutex
	channelID string
	conn      *voice.Conn
	current   *ytTrack
	skip      context.CancelFunc
	queue     []*ytTrack
	next      *ytStream
	fetched   bool
}

type ytStderr struct {
	bytes.Buffer
}

func init() {
	registerCommandHandler("yt", ytCommandHandler)
	registerComponentHandler(ytSelectID, ytSelectHandler)
}

func ytCommandHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.GuildID == "" {
		ytRespond(s, i, true, ytText("This only works in servers."))
		return
	}
	option := i.ApplicationCommandData().Options[0]
	switch option.Name {
	case "play":
		ytPlay(s, i, option.Options[0].StringValue())
	case "next":
		ytNext(s, i)
	case "queue":
		ytQueue(s, i)
	case "move":
		ytMove(s, i)
	}
}

func ytPlay(s *discordgo.Session, i *discordgo.InteractionCreate, query string) {
	channelID := ytVoiceChannel(s, i.GuildID, interactionUserID(i))
	if channelID == "" {
		ytRespond(s, i, true, ytText("Join a voice channel first."))
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Println("Error deferring interaction", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), ytAPITimeout)
	defer cancel()
	id, isLink := ytLinkID(query)
	var videos []ytVideo
	var err error
	if isLink {
		videos, err = ytVideos(ctx, []string{id})
	} else {
		videos, err = ytSearch(ctx, query)
	}

	switch {
	case err != nil:
		log.Println("Error getting YouTube videos", err)
		followup(s, i, ytMessage(ytText("Couldn't reach YouTube, try again later.")))
	case len(videos) == 0 && isLink:
		followup(s, i, ytMessage(ytText("That video doesn't exist or can't be played.")))
	case len(videos) == 0:
		followup(s, i, ytMessage(ytText("No results for **%s**.", ytEscaper.Replace(truncate(query, 100)))))
	case isLink:
		followup(s, i, ytMessage(ytEnqueue(s, i, channelID, videos)...))
	default:
		followup(s, i, ytMessage(ytResults(query, videos)...))
	}
}

func ytSelectHandler(s *discordgo.Session, i *discordgo.InteractionCreate) {
	channelID := ytVoiceChannel(s, i.GuildID, interactionUserID(i))
	if channelID == "" {
		ytRespond(s, i, true, ytText("Join a voice channel first."))
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}); err != nil {
		log.Println("Error deferring interaction", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), ytAPITimeout)
	defer cancel()
	videos, err := ytVideos(ctx, i.MessageComponentData().Values)
	if err != nil || len(videos) == 0 {
		if err != nil {
			log.Println("Error getting YouTube videos", err)
		}
		params := ytMessage(ytText("Couldn't load those videos, try again later."))
		params.Flags |= discordgo.MessageFlagsEphemeral
		followup(s, i, params)
		return
	}
	components := ytContainer(ytEnqueue(s, i, channelID, videos)...)
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Components: &components}); err != nil {
		log.Println("Error editing interaction response", err)
	}
}

func ytNext(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var skipped *ytTrack
	if p := ytPlayerOf(i.GuildID); p != nil {
		p.mu.Lock()
		if p.current != nil {
			skipped = p.current
			p.skip()
		}
		p.mu.Unlock()
	}
	if skipped == nil {
		ytRespond(s, i, true, ytText("Nothing is playing."))
		return
	}
	ytRespond(s, i, false, ytText("Skipped %s.", ytLink(skipped.ytVideo)))
}

func ytQueue(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var current *ytTrack
	var queue []*ytTrack
	var elapsed time.Duration
	if p := ytPlayerOf(i.GuildID); p != nil {
		p.mu.Lock()
		current, queue = p.current, slices.Clone(p.queue)
		p.mu.Unlock()
		elapsed = time.Duration(p.played.Load()) * voice.FrameDuration
	}
	if current == nil && len(queue) == 0 {
		ytRespond(s, i, true, ytText("Nothing is playing."))
		return
	}

	var components []discordgo.MessageComponent
	var left time.Duration
	if current != nil {
		if !current.live {
			left += max(current.duration-elapsed, 0)
		}
		components = append(components, ytText("**Now playing**\n%s `%s / %s` · <@%s>",
			ytLink(current.ytVideo), ytClock(elapsed), ytLength(current.ytVideo), current.requester))
	}
	if len(queue) > 0 {
		for _, t := range queue {
			if !t.live {
				left += t.duration
			}
		}
		if len(components) > 0 {
			components = append(components, discordgo.Separator{})
		}
		components = append(components, ytText("**Up next**\n%s", ytList(queue, true)))
	}
	components = append(components, ytText("-# %d queued · %s left", len(queue), ytClock(left)))
	ytRespond(s, i, false, components...)
}

func ytMove(s *discordgo.Session, i *discordgo.InteractionCreate) {
	channelID := ytVoiceChannel(s, i.GuildID, interactionUserID(i))
	if channelID == "" {
		ytRespond(s, i, true, ytText("Join a voice channel first."))
		return
	}
	var conn *voice.Conn
	p := ytPlayerOf(i.GuildID)
	if p != nil {
		p.mu.Lock()
		conn = p.conn
		p.mu.Unlock()
	}
	switch {
	case conn == nil:
		ytRespond(s, i, true, ytText("Nothing is playing."))
		return
	case conn.ChannelID() == channelID:
		ytRespond(s, i, true, ytText("I'm already in <#%s>.", channelID))
		return
	}
	if problem := ytJoinProblem(s, channelID); problem != "" {
		ytRespond(s, i, true, discordgo.TextDisplay{Content: problem})
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Println("Error deferring interaction", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), ytMoveTimeout)
	defer cancel()
	if err := conn.Move(ctx, channelID); err != nil {
		log.Println("Error moving to voice channel", err)
		followup(s, i, ytMessage(ytText("Couldn't move to <#%s>.", channelID)))
		return
	}
	p.mu.Lock()
	p.channelID = channelID
	p.mu.Unlock()
	followup(s, i, ytMessage(ytText("Moved to <#%s>.", channelID)))
}

func ytEnqueue(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string, videos []ytVideo) []discordgo.MessageComponent {
	tracks := make([]*ytTrack, len(videos))
	for n, v := range videos {
		tracks[n] = &ytTrack{ytVideo: v, requester: interactionUserID(i), textChannelID: i.ChannelID}
	}

	ytPlayersMu.Lock()
	p := ytPlayers[i.GuildID]
	start := p == nil
	if start {
		if problem := ytJoinProblem(s, channelID); problem != "" {
			ytPlayersMu.Unlock()
			return []discordgo.MessageComponent{discordgo.TextDisplay{Content: problem}}
		}
		p = &ytPlayer{guildID: i.GuildID, channelID: channelID}
		ytPlayers[i.GuildID] = p
	}
	p.mu.Lock()
	p.queue = append(p.queue, tracks...)
	if p.fetched {
		p.prefetchLocked(s)
	}
	p.mu.Unlock()
	ytPlayersMu.Unlock()

	if start {
		go p.run(s)
	}
	if len(tracks) == 1 {
		return []discordgo.MessageComponent{ytText("Added %s `%s` to the queue.", ytLink(tracks[0].ytVideo), ytLength(tracks[0].ytVideo))}
	}
	return []discordgo.MessageComponent{ytText("Added **%d** videos to the queue.\n%s", len(tracks), ytList(tracks, false))}
}

func ytPlayerOf(guildID string) *ytPlayer {
	ytPlayersMu.Lock()
	defer ytPlayersMu.Unlock()
	return ytPlayers[guildID]
}

func (p *ytPlayer) run(s *discordgo.Session) {
	for {
		p.mu.Lock()
		channelID := p.channelID
		p.prefetchLocked(s)
		p.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), ytJoinTimeout)
		conn, err := voice.Join(ctx, s, p.guildID, channelID)
		cancel()
		if err != nil {
			log.Println("Error joining voice channel", err)
			p.stop(s, fmt.Sprintf("Couldn't join <#%s>.", channelID))
		} else {
			p.mu.Lock()
			p.conn = conn
			p.mu.Unlock()
			if err := p.playQueue(s, conn); err != nil {
				log.Println("Error playing YouTube audio", err)
				p.stop(s, "I got disconnected from voice, so I cleared the queue.")
			}
			conn.Close()
			p.mu.Lock()
			p.conn = nil
			p.mu.Unlock()
		}
		if p.retire() {
			return
		}
	}
}

func (p *ytPlayer) playQueue(s *discordgo.Session, conn *voice.Conn) error {
	for {
		ctx, track, stream := p.advance(s)
		if track == nil {
			return nil
		}
		err := p.play(s, conn, ctx, track, stream)
		stream.cancel()
		if err != nil {
			return err
		}
		silence, cancel := context.WithTimeout(context.Background(), ytSilenceTimeout)
		conn.Silence(silence)
		cancel()
	}
}

func (p *ytPlayer) advance(s *discordgo.Session) (context.Context, *ytTrack, *ytStream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.skip != nil {
		p.skip()
	}
	p.current, p.skip, p.fetched = nil, nil, false
	if len(p.queue) == 0 {
		return nil, nil, nil
	}

	p.prefetchLocked(s)
	track, stream := p.queue[0], p.next
	p.queue[0] = nil
	p.queue, p.next = p.queue[1:], nil
	ctx, cancel := context.WithCancel(context.Background())
	p.current, p.skip = track, cancel
	p.played.Store(0)
	// The next track only starts downloading once this one has, so the two yt-dlp runs don't
	// compete for CPU. Livestreams never finish downloading.
	p.fetched = track.live
	if p.fetched {
		p.prefetchLocked(s)
	}
	return ctx, track, stream
}

func (p *ytPlayer) play(s *discordgo.Session, conn *voice.Conn, ctx context.Context, track *ytTrack, st *ytStream) error {
	fetched := st.fetched
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-conn.Done():
			return conn.Err()
		case <-fetched:
			fetched = nil
			p.mu.Lock()
			p.fetched = true
			p.prefetchLocked(s)
			p.mu.Unlock()
		case frame, ok := <-st.frames:
			if !ok {
				if st.err != nil && ctx.Err() == nil && p.played.Load() == 0 {
					ytNotify(s, track.textChannelID, fmt.Sprintf("Couldn't play %s: %s", ytLink(track.ytVideo), st.err))
				}
				return nil
			}
			if err := conn.WriteOpus(ctx, frame); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			p.played.Add(1)
		}
	}
}

func (p *ytPlayer) prefetchLocked(s *discordgo.Session) {
	if p.next != nil || len(p.queue) == 0 {
		return
	}
	channelID := p.channelID
	if p.conn != nil {
		channelID = p.conn.ChannelID()
	}
	bitrate := ytDefaultBitrate
	if ch, err := s.State.Channel(channelID); err == nil && ch.Bitrate > 0 {
		bitrate = ch.Bitrate
	}
	p.next = ytStartStream(p.queue[0].id, bitrate)
}

func (p *ytPlayer) stop(s *discordgo.Session, notice string) {
	p.mu.Lock()
	textChannelID := ""
	if p.current != nil {
		textChannelID = p.current.textChannelID
	} else if len(p.queue) > 0 {
		textChannelID = p.queue[0].textChannelID
	}
	if p.skip != nil {
		p.skip()
	}
	if p.next != nil {
		p.next.cancel()
	}
	p.current, p.skip, p.next, p.queue = nil, nil, nil, nil
	p.mu.Unlock()
	if textChannelID != "" {
		ytNotify(s, textChannelID, notice)
	}
}

func (p *ytPlayer) retire() bool {
	ytPlayersMu.Lock()
	defer ytPlayersMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) > 0 {
		return false
	}
	if p.next != nil {
		p.next.cancel()
		p.next = nil
	}
	delete(ytPlayers, p.guildID)
	return true
}

func ytStartStream(id string, bitrate int) *ytStream {
	ctx, cancel := context.WithCancel(context.Background())
	st := &ytStream{frames: make(chan []byte, ytBufferedFrames), fetched: make(chan struct{}), cancel: cancel}
	go func() {
		for attempt := 1; ; attempt++ {
			sent, retry, err := ytDecode(ctx, id, bitrate, st.frames)
			if !retry || sent > 0 || attempt == ytAttempts {
				st.err = err
				break
			}
			log.Printf("Retrying YouTube video %s after attempt %d: %v", id, attempt, err)
		}
		close(st.frames)
		close(st.fetched)
	}()
	return st
}

func ytDecode(ctx context.Context, id string, bitrate int, frames chan<- []byte) (int, bool, error) {
	procCtx, kill := context.WithCancel(ctx)
	defer kill()

	audioR, audioW, err := os.Pipe()
	if err != nil {
		return 0, false, err
	}
	oggR, oggW, err := os.Pipe()
	if err != nil {
		audioR.Close()
		audioW.Close()
		return 0, false, err
	}
	defer oggR.Close()

	var ytdlpErr, ffmpegErr ytStderr
	ytdlp := ytCommand(procCtx, "yt-dlp", "--no-playlist", "--quiet", "--no-warnings",
		"-f", ytFormat, "-o", "-", "--", "https://www.youtube.com/watch?v="+id)
	ytdlp.Stdout, ytdlp.Stderr = audioW, &ytdlpErr
	ffmpeg := ytCommand(procCtx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-map", "0:a:0",
		"-c:a", "libopus", "-b:a", strconv.Itoa(bitrate), "-frame_duration", "20", "-application", "audio",
		"-ar", "48000", "-ac", "2", "-f", "ogg", "pipe:1")
	ffmpeg.Stdin, ffmpeg.Stdout, ffmpeg.Stderr = audioR, oggW, &ffmpegErr

	err = ytdlp.Start()
	if err == nil {
		if err = ffmpeg.Start(); err != nil {
			kill()
			ytdlp.Wait()
		}
	}
	audioR.Close()
	audioW.Close()
	oggW.Close()
	if err != nil {
		return 0, false, err
	}

	sent, readErr := ytReadFrames(ctx, oggR, frames)
	if !errors.Is(readErr, io.EOF) {
		kill()
	}
	ffmpegWait := ffmpeg.Wait()
	ytdlpWait := ytdlp.Wait()
	switch {
	case ctx.Err() != nil:
		return sent, false, ctx.Err()
	case ytdlpWait != nil:
		log.Printf("yt-dlp failed for %s: %v\n%s", id, ytdlpWait, ytdlpErr.String())
		reason, transient := ytReason(ytdlpErr.String())
		return sent, transient, errors.New(reason)
	case ffmpegWait != nil:
		log.Printf("ffmpeg failed for %s: %v\n%s", id, ffmpegWait, ffmpegErr.String())
		return sent, false, errors.New("the audio couldn't be decoded")
	case !errors.Is(readErr, io.EOF):
		return sent, false, readErr
	}
	return sent, false, nil
}

func ytReadFrames(ctx context.Context, r io.Reader, frames chan<- []byte) (int, error) {
	decoder := ogg.NewPacketDecoder(ogg.NewDecoder(bufio.NewReader(r)))
	sent := 0
	for {
		packet, _, err := decoder.Decode()
		if err != nil {
			return sent, err
		}
		if len(packet) == 0 || bytes.HasPrefix(packet, []byte("OpusHead")) || bytes.HasPrefix(packet, []byte("OpusTags")) {
			continue
		}
		select {
		case frames <- packet:
			sent++
		case <-ctx.Done():
			return sent, ctx.Err()
		}
	}
}

// yt-dlp starts its own deno and ffmpeg processes, so cancelling kills the whole process group.
func ytCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = ytWaitDelay
	return cmd
}

func (b *ytStderr) Write(p []byte) (int, error) {
	b.Buffer.Write(p[:min(len(p), max(ytMaxStderr-b.Len(), 0))])
	return len(p), nil
}

// Extractor errors like private or removed videos start with "[youtube] <id>:" and won't go away
// on a retry. Download errors usually will, like the 403s YouTube randomly gives cloud IPs.
func ytReason(stderr string) (string, bool) {
	for _, line := range slices.Backward(strings.Split(strings.TrimSpace(stderr), "\n")) {
		if _, msg, ok := strings.Cut(line, "ERROR: "); ok {
			if !strings.HasPrefix(msg, "[") {
				return truncate(msg, 300), true
			}
			if _, rest, ok := strings.Cut(msg, ": "); ok {
				msg = rest
			}
			return truncate(msg, 300), false
		}
	}
	return "yt-dlp couldn't download it", true
}

func ytAPI(ctx context.Context, endpoint string, params url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/youtube/v3/"+endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Goog-Api-Key", os.Getenv("YOUTUBE_API_KEY"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&body)
		return fmt.Errorf("YouTube %s returned %s: %s", endpoint, res.Status, body.Error.Message)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func ytSearch(ctx context.Context, query string) ([]ytVideo, error) {
	var res struct {
		Items []struct {
			ID struct {
				VideoID string `json:"videoId"`
			} `json:"id"`
		} `json:"items"`
	}
	err := ytAPI(ctx, "search", url.Values{
		"part":       {"id"},
		"type":       {"video"},
		"maxResults": {strconv.Itoa(ytMaxResults)},
		"q":          {query},
		"fields":     {"items/id/videoId"},
	}, &res)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(res.Items))
	for n, item := range res.Items {
		ids[n] = item.ID.VideoID
	}
	return ytVideos(ctx, ids)
}

func ytVideos(ctx context.Context, ids []string) ([]ytVideo, error) {
	ids = slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return !ytIDPattern.MatchString(id) })
	if len(ids) == 0 {
		return nil, nil
	}
	var res struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title                string `json:"title"`
				ChannelTitle         string `json:"channelTitle"`
				LiveBroadcastContent string `json:"liveBroadcastContent"`
			} `json:"snippet"`
			ContentDetails struct {
				Duration string `json:"duration"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	err := ytAPI(ctx, "videos", url.Values{
		"part":   {"snippet,contentDetails"},
		"id":     {strings.Join(ids, ",")},
		"fields": {"items(id,snippet(title,channelTitle,liveBroadcastContent),contentDetails/duration)"},
	}, &res)
	if err != nil {
		return nil, err
	}

	found := make(map[string]ytVideo, len(res.Items))
	for _, item := range res.Items {
		if item.Snippet.LiveBroadcastContent == "upcoming" {
			continue
		}
		found[item.ID] = ytVideo{
			id:       item.ID,
			title:    item.Snippet.Title,
			channel:  item.Snippet.ChannelTitle,
			duration: ytParseDuration(item.ContentDetails.Duration),
			live:     item.Snippet.LiveBroadcastContent == "live",
		}
	}
	videos := make([]ytVideo, 0, len(found))
	for _, id := range ids {
		if v, ok := found[id]; ok {
			videos = append(videos, v)
		}
	}
	return videos, nil
}

func ytParseDuration(iso string) time.Duration {
	var total, n time.Duration
	for _, r := range iso {
		switch {
		case r >= '0' && r <= '9':
			n = n*10 + time.Duration(r-'0')
		case r == 'D':
			total, n = total+n*24*time.Hour, 0
		case r == 'H':
			total, n = total+n*time.Hour, 0
		case r == 'M':
			total, n = total+n*time.Minute, 0
		case r == 'S':
			total, n = total+n*time.Second, 0
		default:
			n = 0
		}
	}
	return total
}

func ytLinkID(query string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(query))
	if err != nil {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	id := ""
	switch host {
	case "youtu.be":
		id = parts[0]
	case "youtube.com", "m.youtube.com", "music.youtube.com":
		switch {
		case parts[0] == "watch":
			id = u.Query().Get("v")
		case len(parts) > 1 && slices.Contains([]string{"shorts", "live", "embed"}, parts[0]):
			id = parts[1]
		}
	}
	return id, ytIDPattern.MatchString(id)
}

func ytVoiceChannel(s *discordgo.Session, guildID, userID string) string {
	if vs, err := s.State.VoiceState(guildID, userID); err == nil {
		return vs.ChannelID
	}
	return ""
}

func ytJoinProblem(s *discordgo.Session, channelID string) string {
	const needed = discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak
	if perms, err := s.State.UserChannelPermissions(s.State.User.ID, channelID); err == nil && perms&needed != needed {
		return fmt.Sprintf("I need permission to connect and speak in <#%s>.", channelID)
	}
	return ""
}

func ytResults(query string, videos []ytVideo) []discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, len(videos))
	for n, v := range videos {
		options[n] = discordgo.SelectMenuOption{
			Label:       truncate(v.title, 100),
			Value:       v.id,
			Description: truncate(ytLength(v)+" · "+v.channel, 100),
		}
	}
	return []discordgo.MessageComponent{
		ytText("Results for **%s**", ytEscaper.Replace(truncate(query, 100))),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    ytSelectID,
			Placeholder: "Choose videos to queue",
			MaxValues:   len(options),
			Options:     options,
		}}},
	}
}

func ytList(tracks []*ytTrack, requesters bool) string {
	var b strings.Builder
	for n, t := range tracks[:min(len(tracks), ytListLength)] {
		fmt.Fprintf(&b, "%d. %s `%s`", n+1, ytLink(t.ytVideo), ytLength(t.ytVideo))
		if requesters {
			fmt.Fprintf(&b, " · <@%s>", t.requester)
		}
		b.WriteByte('\n')
	}
	if more := len(tracks) - ytListLength; more > 0 {
		fmt.Fprintf(&b, "-# …and %d more\n", more)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func ytLink(v ytVideo) string {
	return fmt.Sprintf("[%s](https://youtu.be/%s)", ytEscaper.Replace(v.title), v.id)
}

func ytLength(v ytVideo) string {
	if v.live {
		return "LIVE"
	}
	return ytClock(v.duration)
}

func ytClock(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func ytText(format string, args ...any) discordgo.TextDisplay {
	return discordgo.TextDisplay{Content: fmt.Sprintf(format, args...)}
}

func ytContainer(components ...discordgo.MessageComponent) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: new(ytColor), Components: components}}
}

func ytMessage(components ...discordgo.MessageComponent) *discordgo.WebhookParams {
	return &discordgo.WebhookParams{
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components:      ytContainer(components...),
	}
}

func ytRespond(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool, components ...discordgo.MessageComponent) {
	flags := discordgo.MessageFlagsIsComponentsV2
	if ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:           flags,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
			Components:      ytContainer(components...),
		},
	})
	if err != nil {
		log.Println("Error responding to interaction", err)
	}
}

func ytNotify(s *discordgo.Session, channelID, text string) {
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Flags:           discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Components:      ytContainer(discordgo.TextDisplay{Content: text}),
	})
	if err != nil {
		log.Println("Error sending YouTube notice", err)
	}
}
