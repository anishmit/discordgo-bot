package google

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"golang.org/x/term"
)

//go:embed inspect.js
var inspectJS string

var (
	errStopped = errors.New("sign-in stopped")
	errNoInput = errors.New("no terminal input to sign in with")
)

type page struct {
	URL    string   `json:"url"`
	Busy   bool     `json:"busy"`
	Lines  []string `json:"lines"`
	Fields []struct {
		Selector string `json:"selector"`
		Label    string `json:"label"`
		Secret   bool   `json:"secret"`
	} `json:"fields"`
	Options []struct {
		Selector string `json:"selector"`
		Label    string `json:"label"`
	} `json:"options"`
	raw []byte
}

func (p *page) signedIn() bool {
	return host(p.URL) == signedInHost
}

func (p *page) key() string {
	return fmt.Sprint(p.URL, p.Lines, p.Fields)
}

func signIn(ctx context.Context, p *page) error {
	fmt.Println("\nSign in to Google for /credits. Leave a prompt empty to see more options.")
	for !p.signedIn() {
		fmt.Println()
		for _, line := range p.Lines {
			fmt.Println("  " + line)
		}
		acted, err := respond(ctx, p)
		if err != nil {
			return err
		}
		prev := p
		if !acted {
			prev = nil
		}
		if p, err = settle(ctx, prev); err != nil {
			return err
		}
	}
	fmt.Println("\nSigned in to Google.")
	return nil
}

func respond(ctx context.Context, p *page) (bool, error) {
	if len(p.Fields) == 0 {
		return choose(ctx, p)
	}
	var steps []chromedp.Action[chromedp.Void]
	for i, f := range p.Fields {
		value, err := ask(f.Label, f.Secret)
		if err != nil {
			return false, err
		}
		if value == "" {
			return choose(ctx, p)
		}
		if i == len(p.Fields)-1 {
			value += "\n"
		}
		steps = append(steps,
			chromedp.Evaluate[chromedp.Void](fmt.Sprintf("document.querySelector(%q).value = ''", f.Selector)),
			chromedp.SendKeys(chromedp.CSS(f.Selector), value),
		)
	}
	return act(ctx, steps...), nil
}

func choose(ctx context.Context, p *page) (bool, error) {
	for i, o := range p.Options {
		fmt.Printf("  %d) %s\n", i+1, o.Label)
	}
	fmt.Println("  r) Start over")
	fmt.Println("  s) Save a screenshot of the page")
	fmt.Println("  q) Stop signing in for now")
	for {
		choice, err := ask("Choose, or press Enter to check the page again", false)
		if err != nil {
			return false, err
		}
		switch choice {
		case "":
			return false, nil
		case "r":
			act(ctx, chromedp.Navigate(signInURL))
			return false, nil
		case "s":
			screenshot(ctx)
			continue
		case "q":
			fmt.Printf("If Google won't sign in this browser, start the bot with %s=1 and sign in through the Chrome window it opens.\n", windowEnv)
			return false, errStopped
		}
		if n, err := strconv.Atoi(choice); err == nil && n >= 1 && n <= len(p.Options) {
			return act(ctx, chromedp.Click(chromedp.CSS(p.Options[n-1].Selector))), nil
		}
		fmt.Println("  That isn't one of the options.")
	}
}

func act(ctx context.Context, steps ...chromedp.Action[chromedp.Void]) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := chromedp.Do(ctx, steps...); err != nil {
		fmt.Println("  That didn't work:", err)
		return false
	}
	return true
}

func screenshot(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	png, err := chromedp.Run(ctx, chromedp.CaptureScreenshot())
	if err == nil {
		path, _ := filepath.Abs(filepath.Join(profileDir, "signin.png"))
		if err = os.WriteFile(path, png, 0o600); err == nil {
			fmt.Println("  Saved", path)
			return
		}
	}
	fmt.Println("  Couldn't save a screenshot:", err)
}

func settle(ctx context.Context, prev *page) (*page, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	var last *page
	sawBusy := false
	for stable := 0; ; {
		if p, err := inspect(ctx); err == nil {
			if last != nil && bytes.Equal(p.raw, last.raw) {
				stable++
			} else {
				stable = 0
			}
			last = p
			sawBusy = sawBusy || p.Busy
			moved := prev == nil || sawBusy || p.key() != prev.key() || time.Since(start) > 10*time.Second
			if p.signedIn() || !p.Busy && stable >= 2 && moved {
				return p, nil
			}
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return last, nil
			}
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func inspect(ctx context.Context) (*page, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[[]byte](inspectJS))
	if err != nil {
		return nil, err
	}
	p := &page{raw: raw}
	return p, json.Unmarshal(raw, p)
}

func ask(label string, secret bool) (string, error) {
	fmt.Print(label + ": ")
	fd := int(os.Stdin.Fd())
	if secret && term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Println()
		return string(b), err
	}
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if n == 1 && b[0] == '\n' {
			break
		}
		line = append(line, b[:n]...)
		if errors.Is(err, io.EOF) && len(line) > 0 {
			break
		}
		if errors.Is(err, io.EOF) {
			fmt.Println()
			return "", errNoInput
		}
		if err != nil {
			return "", err
		}
	}
	value := strings.TrimSuffix(string(line), "\r")
	if !secret {
		value = strings.TrimSpace(value)
	}
	return value, nil
}
