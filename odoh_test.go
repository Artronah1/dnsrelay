package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestParseAllODoH(t *testing.T) {
	files := []string{"odoh-relays.md", "odoh-servers.md"}

	for _, filename := range files {
		fmt.Printf("\n=== %s ===\n", filename)

		file, err := os.Open(filename)
		if err != nil {
			t.Fatalf("open %s: %v", filename, err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		currentName := ""
		total, ok, fail := 0, 0, 0

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())

			if strings.HasPrefix(line, "## ") {
				currentName = strings.TrimPrefix(line, "## ")
				continue
			}

			if strings.HasPrefix(line, "sdns://") {
				total++
				parsed, err := ParseODoHStamp(line)
				if err != nil {
					fail++
					fmt.Printf("  [FAIL] %s: %v\n", currentName, err)
					continue
				}
				ok++
				// Print the first 5 successes as an example
				if ok <= 5 {
					fmt.Printf("  [OK]   %s -> %s%s\n",
						   currentName, parsed.Hostname, parsed.Path)
				}
			}
		}

		fmt.Printf("Summary: total=%d ok=%d fail=%d\n", total, ok, fail)
	}
}
