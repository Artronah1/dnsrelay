package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// runODoHPipeline — two-stage check of ODoH relays and targets.
func runODoHPipeline() {
	fmt.Println("[*] ODoH mode, two-stage pipeline")

	// --- Read relays ---
	relayEntries, err := readODoHFile(odohRelaysFile)
	if err != nil {
		fmt.Printf("Error reading relays: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[*] Relays found: %d\n", len(relayEntries))

	// --- Read targets ---
	targetEntries, err := readODoHFile(odohServersFile)
	if err != nil {
		fmt.Printf("Error reading targets: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[*] Targets found: %d\n", len(targetEntries))

	// --- Find the test target for Stage 1 ---
	var testTarget *odohEntry
	for i := range targetEntries {
		if targetEntries[i].Name == odohTestTarget {
			testTarget = &targetEntries[i]
			break
		}
	}
	if testTarget == nil {
		fmt.Printf("Test target '%s' not found in the list\n", odohTestTarget)
		os.Exit(1)
	}
	fmt.Printf("[*] Test target for Stage 1: %s (%s%s)\n",
		testTarget.Name, testTarget.Parsed.Hostname, testTarget.Parsed.Path)

	// ============ STAGE 1: working relays ============
	fmt.Println("\n=== Stage 1: checking relays ===")

	liveRelays := stage1_checkRelays(relayEntries, testTarget)
	fmt.Printf("\n[*] Live relays: %d of %d\n", len(liveRelays), len(relayEntries))

	if len(liveRelays) == 0 {
		fmt.Println("No live relays, nowhere to go from here.")
		return
	}

	// ============ STAGE 2: targets through live relays ============
	fmt.Println("\n=== Stage 2: iterating over targets through live relays ===")

	out, err := os.Create(odohOutFile)
	if err != nil {
		fmt.Printf("Error creating results file: %v\n", err)
		os.Exit(1)
	}
	defer out.Close()

	total := 0
	for _, relay := range liveRelays {
		fmt.Printf("\n--- Relay: %s (%s%s) ---\n",
			relay.Name, relay.Parsed.Hostname, relay.Parsed.Path)
		results := stage2_checkTargets(relay, targetEntries)
		for _, r := range results {
			fmt.Fprintln(out, r)
		}
		total += len(results)
	}

	fmt.Printf("\n[*] Done. Working relay/target pairs found: %d\n", total)
	fmt.Printf("[*] Result file: %s\n", odohOutFile)
	fmt.Println("[*] Line format: <relay>|<target>|<latency_ms>")
}

// odohEntry — an entry from odoh-relays.md or odoh-servers.md.
type odohEntry struct {
	Name   string
	Stamp  string
	Parsed *ODoHStamp
}

// readODoHFile reads a file of the form ## name + sdns://... and returns a list of entries.
func readODoHFile(path string) ([]odohEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []odohEntry
	scanner := bufio.NewScanner(file)
	currentName := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "## ") {
			currentName = strings.TrimPrefix(line, "## ")
			continue
		}

		if strings.HasPrefix(line, "sdns://") {
			parsed, err := ParseODoHStamp(line)
			if err != nil {
				// Skip unparsable stamps silently or with a log
				continue
			}
			entries = append(entries, odohEntry{
				Name:   currentName,
				Stamp:  line,
				Parsed: parsed,
			})
			currentName = ""
		}
	}

	return entries, scanner.Err()
}

// stage1_checkRelays checks all relays through a single test target.
// Returns the list of live relays.
func stage1_checkRelays(relays []odohEntry, testTarget *odohEntry) []odohEntry {
	var live []odohEntry
	var mu sync.Mutex
	var wg sync.WaitGroup

	jobs := make(chan odohEntry, len(relays))
	sem := make(chan struct{}, workers) // concurrency limit

	for _, r := range relays {
		jobs <- r
	}
	close(jobs)

	for r := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(relay odohEntry) {
			defer wg.Done()
			defer func() { <-sem }()

			ok, latency, err := CheckODoHTargetViaRelay(
				testTarget.Parsed.Hostname,
				testTarget.Parsed.Path,
				relay.Parsed.Hostname,
				relay.Parsed.Path,
				timeout,
			)

			mu.Lock()
			defer mu.Unlock()

			if ok {
				fmt.Printf("[+] %s (%s): %.0f ms\n",
					relay.Name, relay.Parsed.Hostname, latency.Seconds()*1000)
				live = append(live, relay)
			} else if verbose {
				fmt.Printf("[-] %s (%s): %v\n",
					relay.Name, relay.Parsed.Hostname, err)
			}
		}(r)
	}

	wg.Wait()
	return live
}

// stage2_checkTargets iterates over all targets through a specific relay.
// Returns result lines in the format "<relay>|<target>|<latency_ms>".
func stage2_checkTargets(relay odohEntry, targets []odohEntry) []string {
	var results []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	jobs := make(chan odohEntry, len(targets))
	sem := make(chan struct{}, workers)

	for _, t := range targets {
		jobs <- t
	}
	close(jobs)

	done := 0
	total := len(targets)

	for t := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(target odohEntry) {
			defer wg.Done()
			defer func() { <-sem }()

			ok, latency, err := CheckODoHTargetViaRelay(
				target.Parsed.Hostname,
				target.Parsed.Path,
				relay.Parsed.Hostname,
				relay.Parsed.Path,
				timeout,
			)

			mu.Lock()
			done++
			if ok {
				ms := latency.Seconds() * 1000
				fmt.Printf("[+] %s: %.0f ms\n", target.Name, ms)
				results = append(results, fmt.Sprintf("%s|%s|%.0f",
					relay.Name, target.Name, ms))
			} else if verbose && done%20 == 0 {
				fmt.Printf("    ... %d/%d checked\n", done, total)
			}
			mu.Unlock()

			_ = err
		}(t)
	}

	wg.Wait()
	return results
}
