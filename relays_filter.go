package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ameshkov/dnscrypt/v2"
)

// aliveRelayMu protects file writes from races between workers.
var aliveRelayMu sync.Mutex

// writeAliveRelay appends a live relay to the file in the format ## name + sdns://.
func writeAliveRelay(path string, entry RelayEntry) {
	aliveRelayMu.Lock()
	defer aliveRelayMu.Unlock()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "## %s\n%s\n\n", entry.Name, entry.Stamp)
}

// readRelaysFileSimple reads a file of the form ## name + sdns://... and returns a list of relays.
func readRelaysFileSimple(path string) ([]RelayEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []RelayEntry
	scanner := bufio.NewScanner(file)
	currentName := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "## ") {
			currentName = strings.TrimPrefix(line, "## ")
			continue
		}
		if strings.HasPrefix(line, "sdns://") {
			out = append(out, RelayEntry{Name: currentName, Stamp: line})
			currentName = ""
		}
	}

	return out, scanner.Err()
}

// runRelaysFilter filters live relays and writes them to outFile.
// Used in the auto pipeline.
func runRelaysFilter(relaysFile, outFile string) {
	relays, err := readRelaysFileSimple(relaysFile)
	if err != nil {
		fmt.Printf("Error reading %s: %v\n", relaysFile, err)
		return
	}
	fmt.Printf("[*] Relays in input: %d\n", len(relays))

	if err := os.WriteFile(outFile, []byte{}, 0644); err != nil {
		fmt.Printf("Error creating %s: %v\n", outFile, err)
		return
	}

	globalClient = &dnscrypt.Client{Net: "udp", Timeout: timeout}
	var dialErr error
	globalResolverInfo, dialErr = globalClient.Dial(resolverStamp)
	if dialErr != nil {
		fmt.Printf("Dial error: %v\n", dialErr)
		return
	}

	jobs := make(chan RelayEntry, len(relays))
	var wg sync.WaitGroup
	var liveCount int64

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range jobs {
				addr, err := DecodeRelayStamp(entry.Stamp)
				if err != nil {
					continue
				}
				useTCP := proto == "tcp"
				ok, _, _ := checkRelayDNSCrypt(addr, resolverStamp, timeout, useTCP)
				if ok {
					writeAliveRelay(outFile, entry)
					atomic.AddInt64(&liveCount, 1)
				}
			}
		}()
	}

	for _, r := range relays {
		jobs <- r
	}
	close(jobs)
	wg.Wait()

	fmt.Printf("[*] Live relays: %d of %d\n", atomic.LoadInt64(&liveCount), len(relays))
	fmt.Printf("[*] Written to %s\n", outFile)
}
