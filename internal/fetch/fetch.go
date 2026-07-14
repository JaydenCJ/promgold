// Package fetch resolves an exposition source argument into bytes. Three
// forms are accepted: "-" for stdin, "http://" / "https://" for a live
// scrape of a user-named endpoint (the only network promgold ever touches),
// and anything else as a file path.
package fetch

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultTimeout bounds a live scrape so a hung endpoint cannot hang CI.
const DefaultTimeout = 10 * time.Second

// Read resolves source into its raw bytes. stdin supplies the reader used
// for the "-" form, so tests can inject one.
func Read(source string, stdin io.Reader, timeout time.Duration) ([]byte, error) {
	switch {
	case source == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return data, nil
	case strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://"):
		return scrape(source, timeout)
	default:
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", source, err)
		}
		return data, nil
	}
}

// scrape performs one GET against a metrics endpoint. No redirect chasing
// beyond Go's default, no retries, no headers beyond Accept: a contract
// check should see exactly what a Prometheus scrape sees.
func scrape(url string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("scraping %s: %w", url, err)
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraping %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scraping %s: endpoint returned %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("scraping %s: %w", url, err)
	}
	return data, nil
}
