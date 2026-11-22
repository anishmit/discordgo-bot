package google

import (
	"context"
	"errors"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

const (
	signInURL     = "https://accounts.google.com/ServiceLogin?continue=https%3A%2F%2Fmyaccount.google.com%2F"
	signedInHost  = "myaccount.google.com"
	accountsHost  = "accounts.google.com"
	checkInterval = time.Hour
	profileDir    = ".google-profile"
	windowEnv     = "GOOGLE_SIGNIN_WINDOW"
)

var ErrSignedOut = errors.New("not signed in to Google")

var (
	lock     = make(chan struct{}, 1)
	wake     = make(chan struct{}, 1)
	signedIn atomic.Bool
	tab      context.Context
	stopTab  context.CancelFunc
)

func Start() {
	if err := check(); err != nil {
		log.Println("Google sign-in:", err)
	}
	go func() {
		ticker := time.NewTicker(checkInterval)
		for {
			select {
			case <-ticker.C:
			case <-wake:
			}
			if err := check(); err != nil {
				log.Println("Google sign-in:", err)
			}
		}
	}()
}

func recheck() {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func Close() {
	select {
	case lock <- struct{}{}:
	case <-time.After(5 * time.Second):
		return
	}
	if tab == nil {
		return
	}
	ctx, cancel := context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	chromedp.Cancel(ctx)
	stopTab()
}

func check() error {
	lock <- struct{}{}
	defer func() { <-lock }()

	ctx, err := openTab()
	if err != nil {
		return err
	}
	p, err := load(ctx)
	if err != nil {
		return err
	}
	if p.signedIn() {
		signedIn.Store(true)
		return nil
	}
	signedIn.Store(false)
	if err := signIn(ctx, p); err != nil {
		return err
	}
	signedIn.Store(true)
	return nil
}

func host(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

func load(ctx context.Context) (*page, error) {
	navCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := chromedp.Do(navCtx, chromedp.Navigate(signInURL)); err != nil {
		return nil, err
	}
	return settle(ctx, nil)
}

func openTab() (context.Context, error) {
	if tab != nil && tab.Err() == nil {
		return tab, nil
	}
	if stopTab != nil {
		stopTab()
		tab, stopTab = nil, nil
	}
	dir, err := filepath.Abs(profileDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(dir),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	if os.Getenv(windowEnv) != "" {
		opts = append(opts, chromedp.VisibleWindow)
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithRuntimeDisabled())
	stop := func() {
		cancelCtx()
		cancelAlloc()
	}
	if err := chromedp.Do(ctx); err != nil {
		stop()
		return nil, err
	}
	if err := hideHeadless(ctx); err != nil {
		stop()
		return nil, err
	}
	tab, stopTab = ctx, stop
	return tab, nil
}

// Google refuses to sign in browsers that call themselves HeadlessChrome. The client hints have to be
// copied from a secure page such as chrome://version, or Chrome stops sending them with the new user agent.
func hideHeadless(ctx context.Context) error {
	ver, err := chromedp.CallBrowser(ctx, browser.GetVersion, cdp.Empty{})
	if err != nil {
		return err
	}
	ua := strings.ReplaceAll(ver.UserAgent, "HeadlessChrome", "Chrome")
	if ua == ver.UserAgent {
		return nil
	}
	if err := chromedp.Do(ctx, chromedp.Navigate("chrome://version/")); err != nil {
		return err
	}
	metadata, err := chromedp.Run(ctx, chromedp.Evaluate[*emulation.UserAgentMetadata](
		`navigator.userAgentData.getHighEntropyValues(["architecture", "bitness", "formFactors", "fullVersionList", "model", "platformVersion", "wow64"])`,
		chromedp.EvalAwaitPromise,
	))
	if err != nil {
		return err
	}
	_, err = chromedp.Call(ctx, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{UserAgent: ua, UserAgentMetadata: metadata})
	return err
}
