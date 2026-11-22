package google

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func Watch(ctx context.Context, pageURL string, match func(body []byte) bool) error {
	if !signedIn.Load() {
		return ErrSignedOut
	}
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-lock }()
	if !signedIn.Load() {
		return ErrSignedOut
	}

	tabCtx, err := openTab()
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(tabCtx, 5*time.Second)
		defer cancel()
		chromedp.Call(ctx, cdppage.Navigate, cdppage.NavigateParams{URL: "about:blank"})
	}()
	run, cancel := context.WithCancel(tabCtx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	err = watch(run, pageURL, match)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func watch(ctx context.Context, pageURL string, match func([]byte) bool) error {
	var (
		mu        sync.Mutex
		loaded    []network.RequestID
		signedOut bool
		ready     = make(chan struct{}, 1)
		requests  = map[network.RequestID]bool{}
	)
	err := chromedp.Listen(ctx, func(ev any) {
		mu.Lock()
		defer mu.Unlock()
		switch ev := ev.(type) {
		case *network.EventResponseReceived:
			if ev.Type == network.ResourceTypeXHR || ev.Type == network.ResourceTypeFetch {
				requests[ev.RequestID] = true
			}
			return
		case *network.EventLoadingFinished:
			if !requests[ev.RequestID] {
				return
			}
			delete(requests, ev.RequestID)
			loaded = append(loaded, ev.RequestID)
		case *cdppage.EventFrameNavigated:
			if ev.Frame.ParentID != "" || host(ev.Frame.URL) != accountsHost {
				return
			}
			signedOut = true
		default:
			return
		}
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return err
	}

	nav, err := chromedp.Call(ctx, cdppage.Navigate, cdppage.NavigateParams{URL: pageURL})
	if err != nil {
		return err
	}
	if nav.ErrorText != "" {
		return fmt.Errorf("opening %s: %s", pageURL, nav.ErrorText)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ready:
		}
		mu.Lock()
		ids, out := loaded, signedOut
		loaded = nil
		mu.Unlock()
		if out {
			signedIn.Store(false)
			recheck()
			return ErrSignedOut
		}
		for _, id := range ids {
			res, err := chromedp.Call(ctx, network.GetResponseBody, network.GetResponseBodyParams{RequestID: id})
			if err == nil && match(res.Body) {
				return nil
			}
		}
	}
}
