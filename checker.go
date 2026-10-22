package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Result struct {
	URL      string `json:"url"`
	FinalURL string `json:"final_url,omitempty"`
	Status   int    `json:"status,omitempty"`
	Outcome  string `json:"outcome"`
	Error    string `json:"error,omitempty"`
}

func readURLs(input io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	seen := make(map[string]bool)
	var urls []string
	line := 0

	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		parsed, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid URL: %w", line, err)
		}
		if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Hostname() == "" || parsed.User != nil {
			return nil, fmt.Errorf("line %d: expected an absolute HTTP(S) URL without credentials", line)
		}

		parsed.Fragment = ""
		parsed.RawFragment = ""
		normalised := parsed.String()
		if seen[normalised] {
			continue
		}

		seen[normalised] = true
		urls = append(urls, normalised)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read URLs: %w", err)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("input contains no URLs")
	}

	return urls, nil
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("redirect limit reached")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect uses an unsupported scheme")
			}
			if req.URL.User != nil {
				return fmt.Errorf("redirect contains credentials")
			}
			return nil
		},
	}
}

func checkURL(ctx context.Context, client *http.Client, target string) Result {
	result := Result{URL: target, Outcome: "review"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "article-linkcheck/1.0")

	response, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()

	result.Status = response.StatusCode
	result.FinalURL = response.Request.URL.String()

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.Outcome = "ok"
	case response.StatusCode == http.StatusNotFound ||
		response.StatusCode == http.StatusGone:
		result.Outcome = "broken"
	}

	return result
}

func checkAll(
	ctx context.Context,
	client *http.Client,
	urls []string,
	workers int,
	interval time.Duration,
) <-chan Result {
	jobs := make(chan string)
	results := make(chan Result)
	var running sync.WaitGroup
	running.Add(workers + 1)

	go func() {
		defer running.Done()
		defer close(jobs)

		for _, target := range urls {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			select {
			case <-ctx.Done():
				return
			case jobs <- target:
			}
		}
	}()

	for i := 0; i < workers; i++ {
		go func() {
			defer running.Done()

			for target := range jobs {
				if ctx.Err() != nil {
					return
				}
				result := checkURL(ctx, client, target)
				select {
				case <-ctx.Done():
					return
				case results <- result:
				}
			}
		}()
	}

	go func() {
		running.Wait()
		close(results)
	}()

	return results
}
