package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFetchODoHConfigs(t *testing.T) {
	file, err := os.Open("odoh-servers.md")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	scanner := bufio.NewScanner(file)
	currentName := ""
	count := 0
	maxTest := 5 // only check the first 5 targets

	for scanner.Scan() && count < maxTest {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "## ") {
			currentName = strings.TrimPrefix(line, "## ")
			continue
		}

		if strings.HasPrefix(line, "sdns://") {
			parsed, err := ParseODoHStamp(line)
			if err != nil {
				fmt.Printf("  [PARSE FAIL] %s: %v\n", currentName, err)
				continue
			}

			url := fmt.Sprintf("https://%s/.well-known/odohconfigs", parsed.Hostname)
			req, _ := http.NewRequest("GET", url, nil)
			resp, err := client.Do(req)
			if err != nil {
				fmt.Printf("  [HTTP FAIL] %s (%s): %v\n", currentName, parsed.Hostname, err)
				count++
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			fmt.Printf("  [%d] %s (%s): status=%d, body_len=%d\n",
				   resp.StatusCode, currentName, parsed.Hostname, resp.StatusCode, len(body))

			count++
		}
	}
}
