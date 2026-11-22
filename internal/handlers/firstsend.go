package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/anishmit/discordgo-bot/internal/database"
)

const (
	calibrationChannelID = "1478145744702669043"
	sendsPerAccount      = 5
	calibrationRounds    = 6
	calibrationInterval  = 6 * time.Second
	firstPrepareLead     = 2 * time.Minute
	firstSendTimeout     = 3 * time.Second
	firstDBTimeout       = 5 * time.Second
	spinWindow           = 20 * time.Millisecond
	lastSendMargin       = 5 * time.Millisecond
	fallbackMinLag       = 50 * time.Millisecond
	fallbackMaxLag       = 150 * time.Millisecond
	biasNights           = 7
	minBiasNights        = 3
)

const (
	createFirstBiasSQL = `CREATE TABLE IF NOT EXISTS first_send_bias (
	iso_date date NOT NULL,
	sender text NOT NULL,
	bias_us bigint NOT NULL,
	PRIMARY KEY (iso_date, sender)
)`
	selectFirstBiasSQL = `SELECT COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY bias_us), 0), count(*)
FROM (SELECT bias_us FROM first_send_bias WHERE sender = $1 ORDER BY iso_date DESC LIMIT $2) recent`
	upsertFirstBiasSQL = `INSERT INTO first_send_bias (iso_date, sender, bias_us) VALUES ($1, $2, $3) ON CONFLICT (iso_date, sender) DO UPDATE SET bias_us = EXCLUDED.bias_us`
)

type firstSender struct {
	name, auth string
	clients    []*http.Client
}

type firstSend struct {
	lead          time.Duration
	sent, stamped time.Time
	err           error
}

var firstSendOnce sync.Once

func init() {
	registerReadyHandler(firstSendReadyHandler)
}

func firstSendReadyHandler(s *discordgo.Session, r *discordgo.Ready) {
	firstSendOnce.Do(func() { go runFirstSends(firstSenders()) })
}

func firstSenders() []firstSender {
	senders := []firstSender{newFirstSender("bot", "Bot "+os.Getenv("BOT_TOKEN"))}
	if token := os.Getenv("USER_TOKEN"); token != "" {
		return append([]firstSender{newFirstSender("user", token)}, senders...)
	}
	log.Println("first: USER_TOKEN is not set, sending from the bot only")
	return senders
}

// Every send gets its own connection, so a lost packet on one can't hold up the others.
func newFirstSender(name, auth string) firstSender {
	sender := firstSender{name: name, auth: auth}
	for range sendsPerAccount {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		sender.clients = append(sender.clients, &http.Client{Timeout: firstSendTimeout, Transport: transport})
	}
	return sender
}

func runFirstSends(senders []firstSender) {
	if _, err := database.Pool.Exec(context.Background(), createFirstBiasSQL); err != nil {
		log.Println("Error creating first send bias table", err)
	}
	for midnight := nextMidnight(time.Now()); ; midnight = nextMidnight(midnight) {
		sleepUntil(midnight.Add(-firstPrepareLead))
		offset := discordClockOffset(senders[0].clients[0])
		if time.Now().Add(offset).Before(midnight) {
			sendFirstMessages(senders, midnight, offset)
		}
	}
}

func nextMidnight(t time.Time) time.Time {
	y, m, d := t.In(curLocation).Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, curLocation)
}

func discordClockOffset(client *http.Client) time.Duration {
	start := time.Now()
	resp, err := client.Get(discordgo.EndpointGateway)
	if err != nil {
		return 0
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return 0
	}
	offset := date.Add(time.Second / 2).Sub(start.Add(time.Since(start) / 2))
	if offset.Abs() < time.Second {
		return 0
	}
	direction := "behind"
	if offset < 0 {
		direction = "ahead of"
	}
	log.Printf("first: this machine's clock is %v %s Discord's, shifting calibration to match", offset.Abs().Round(100*time.Millisecond), direction)
	return offset
}

func sendFirstMessages(senders []firstSender, midnight time.Time, offset time.Duration) {
	biases := loadFirstBiases(senders)
	localMidnight := midnight.Add(-offset)
	sleepUntil(localMidnight.Add(-calibrationRounds*calibrationInterval - time.Second))
	warmUpFirstSenders(senders)

	var rounds [][][]time.Duration
	for r := range calibrationRounds {
		// Rounds are spaced by their first send so the rate limit window is clear even when the lead jumps.
		start := localMidnight.Add(-time.Duration(calibrationRounds-r) * calibrationInterval)
		if time.Now().After(start) {
			continue
		}
		leads := planFirstSends(len(senders), rounds, nil)
		fakeMidnight := start.Add(slices.Max(slices.Concat(leads...)))
		results := sendFirstBurst(senders, calibrationChannelID, fakeMidnight, leads)
		logFirstBurst(fmt.Sprintf("calibration %d", r+1), senders, fakeMidnight, results)
		rounds = append(rounds, firstLags(results))
	}

	results := sendFirstBurst(senders, channelID, midnight, planFirstSends(len(senders), rounds, biases))
	logFirstBurst("midnight", senders, midnight, results)
	saveFirstBiases(senders, midnight, rounds, results)
}

func warmUpFirstSenders(senders []firstSender) {
	var wg sync.WaitGroup
	for _, sender := range senders {
		for _, client := range sender.clients {
			wg.Go(func() {
				if resp, err := client.Get(discordgo.EndpointGateway); err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
			})
		}
	}
	wg.Wait()
}

// Sends get spread lag percentiles, highest first and interleaved across senders: a send at
// percentile q lands before midnight with probability q. The last one gets an extra margin that
// widens when the calibration rounds disagree.
func planFirstSends(senders int, rounds [][][]time.Duration, biases []time.Duration) [][]time.Duration {
	lags := senderLags(senders, rounds)
	pooled := slices.Sorted(slices.Values(slices.Concat(lags...)))
	margin := max(lastSendMargin, roundSpread(rounds, lags))
	levels := senders * sendsPerAccount
	leads := make([][]time.Duration, senders)
	for a, l := range lags {
		sorted := pooled
		if len(l) > 0 {
			sorted = slices.Sorted(slices.Values(l))
		}
		for k := range sendsPerAccount {
			q := float64((sendsPerAccount-1-k)*senders+a) / float64(levels)
			lead := lagPercentile(sorted, q)
			if q == 0 {
				lead -= margin
			}
			if biases != nil {
				lead += biases[a]
			}
			leads[a] = append(leads[a], lead)
		}
	}
	return leads
}

func senderLags(senders int, rounds [][][]time.Duration) [][]time.Duration {
	lags := make([][]time.Duration, senders)
	for _, round := range rounds {
		for a, l := range round {
			lags[a] = append(lags[a], l...)
		}
	}
	return lags
}

func roundSpread(rounds [][][]time.Duration, lags [][]time.Duration) time.Duration {
	medians := make([]time.Duration, len(lags))
	for a, l := range lags {
		if len(l) > 0 {
			medians[a] = medianLag(l)
		}
	}
	var shifts []time.Duration
	for _, round := range rounds {
		var residuals []time.Duration
		for a, l := range round {
			for _, lag := range l {
				residuals = append(residuals, lag-medians[a])
			}
		}
		if len(residuals) > 0 {
			shifts = append(shifts, medianLag(residuals))
		}
	}
	if len(shifts) == 0 {
		return 0
	}
	return slices.Max(shifts) - slices.Min(shifts)
}

func medianLag(lags []time.Duration) time.Duration {
	return slices.Sorted(slices.Values(lags))[len(lags)/2]
}

func lagPercentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return fallbackMinLag + time.Duration(q*float64(fallbackMaxLag-fallbackMinLag))
	}
	return sorted[int(math.Round(q*float64(len(sorted)-1)))]
}

func firstLags(results [][]firstSend) [][]time.Duration {
	lags := make([][]time.Duration, len(results))
	for a, rs := range results {
		for _, res := range rs {
			if res.err == nil {
				lags[a] = append(lags[a], res.stamped.Sub(res.sent))
			}
		}
	}
	return lags
}

// The real channel can lag differently from the calibration channel, so each night records the
// difference and later nights shift by the median of the recent ones.
func loadFirstBiases(senders []firstSender) []time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), firstDBTimeout)
	defer cancel()
	biases := make([]time.Duration, len(senders))
	var applied []string
	for a, sender := range senders {
		var biasUS int64
		var nights int
		if err := database.Pool.QueryRow(ctx, selectFirstBiasSQL, sender.name, biasNights).Scan(&biasUS, &nights); err != nil {
			log.Println("Error loading first send bias", err)
			continue
		}
		if nights >= minBiasNights {
			biases[a] = time.Duration(biasUS) * time.Microsecond
			applied = append(applied, fmt.Sprintf("%s %+d ms", sender.name, biases[a].Milliseconds()))
		}
	}
	if len(applied) > 0 {
		log.Printf("first: correcting for the real channel by %s", strings.Join(applied, ", "))
	}
	return biases
}

func saveFirstBiases(senders []firstSender, midnight time.Time, rounds [][][]time.Duration, results [][]firstSend) {
	ctx, cancel := context.WithTimeout(context.Background(), firstDBTimeout)
	defer cancel()
	calibration, actual := senderLags(len(senders), rounds), firstLags(results)
	var tonight []string
	for a, sender := range senders {
		if len(calibration[a]) == 0 || len(actual[a]) == 0 {
			continue
		}
		bias := medianLag(actual[a]) - medianLag(calibration[a])
		tonight = append(tonight, fmt.Sprintf("%s %+d ms", sender.name, bias.Milliseconds()))
		if _, err := database.Pool.Exec(ctx, upsertFirstBiasSQL, midnight.Format(time.DateOnly), sender.name, bias.Microseconds()); err != nil {
			log.Println("Error saving first send bias", err)
		}
	}
	if len(tonight) > 0 {
		log.Printf("first: real channel lagged calibration by %s tonight", strings.Join(tonight, ", "))
	}
}

func sendFirstBurst(senders []firstSender, channel string, midnight time.Time, leads [][]time.Duration) [][]firstSend {
	results := make([][]firstSend, len(senders))
	var wg sync.WaitGroup
	for a, sender := range senders {
		results[a] = make([]firstSend, len(leads[a]))
		for k, lead := range leads[a] {
			wg.Go(func() {
				res := firstSend{lead: lead}
				res.sent, res.stamped, res.err = postFirstMessage(sender.clients[k], sender.auth, channel, k+1, midnight.Add(-lead))
				results[a][k] = res
			})
		}
	}
	wg.Wait()
	return results
}

func postFirstMessage(client *http.Client, auth, channel string, n int, at time.Time) (sent, stamped time.Time, err error) {
	body := fmt.Sprintf(`{"content":"Scheduled message %d sent."}`, n)
	req, err := http.NewRequest(http.MethodPost, discordgo.EndpointChannelMessages(channel), strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")

	sleepUntil(at)
	sent = time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("%s: %s", resp.Status, data)
		return
	}
	var msg struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(data, &msg); err != nil {
		return
	}
	stamped, err = discordgo.SnowflakeTimestamp(msg.ID)
	return
}

func sleepUntil(t time.Time) {
	for d := time.Until(t); d > spinWindow; d = time.Until(t) {
		time.Sleep(min(d-spinWindow, time.Minute))
	}
	for time.Now().Before(t) {
		runtime.Gosched()
	}
}

func logFirstBurst(label string, senders []firstSender, midnight time.Time, results [][]firstSend) {
	for a, rs := range results {
		sent := make([]string, len(rs))
		landed := make([]string, len(rs))
		var firstErr error
		for k, res := range rs {
			sent[k] = fmt.Sprintf("%+d", (-res.lead).Milliseconds())
			if res.err != nil {
				landed[k] = "error"
				if firstErr == nil {
					firstErr = res.err
				}
				continue
			}
			landed[k] = fmt.Sprintf("%+d", res.stamped.Sub(midnight).Milliseconds())
		}
		line := fmt.Sprintf("first: %s %s: sent %s ms, landed %s ms", label, senders[a].name, strings.Join(sent, "/"), strings.Join(landed, "/"))
		if firstErr != nil {
			line += fmt.Sprintf(" (%v)", firstErr)
		}
		log.Println(line)
	}
}
