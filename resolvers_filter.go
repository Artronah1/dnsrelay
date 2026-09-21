package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ameshkov/dnscrypt/v2"
)

// runResolversFilter checks all resolvers without a relay — only dial + a test query.
// The result is a file of live resolvers in the format ## name + sdns://.
func runResolversFilter(resolversFile, outFile string, excludeIPv6 bool) {
	resolvers, err := readResolversFile(resolversFile)
	if err != nil {
		fmt.Printf("Read error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[*] Resolvers in input: %d\n", len(resolvers))
	fmt.Printf("[*] Workers: %d, timeout: %s\n", workers, timeout)
	fmt.Println("[*] Checking dial to each resolver...")

	type result struct {
		Entry ResolverEntry
		OK    bool
		LatencyMs float64
		Err   string
	}

	jobs := make(chan ResolverEntry, len(resolvers))
	results := make(chan result, len(resolvers))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range jobs {
				res := result{Entry: r}
				start := time.Now()

				client := &dnscrypt.Client{Net: "udp", Timeout: timeout}
				_, err := client.Dial(r.Stamp)
				res.LatencyMs = float64(time.Since(start).Milliseconds())
				if err != nil {
					res.Err = err.Error()
				} else {
					res.OK = true
				}
				results <- res
			}
		}()
	}

	go func() {
		for _, r := range resolvers {
			jobs <- r
		}
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect the live ones
	var live []ResolverEntry
	var dead []ResolverEntry
	for r := range results {
		if r.OK {
			live = append(live, r.Entry)
			fmt.Printf("[+] %s (%.0f ms)\n", r.Entry.Name, r.LatencyMs)
		} else {
			dead = append(dead, r.Entry)
			if verbose {
				fmt.Printf("[-] %s: %s\n", r.Entry.Name, r.Err)
			}
		}
	}

	fmt.Printf("\n[*] Live: %d of %d\n", len(live), len(resolvers))
	fmt.Printf("[*] Dead: %d\n", len(dead))

	// Write the live ones to a file
	out, err := os.Create(outFile)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	defer out.Close()
	w := bufio.NewWriter(out)
	defer w.Flush()

	for _, r := range live {
		fmt.Fprintf(w, "## %s\n%s\n\n", r.Name, r.Stamp)
	}

	fmt.Printf("[*] Live resolvers written to %s\n", outFile)
}

// Stub so the compiler doesn't complain about the flag import
var _ = flag.String
