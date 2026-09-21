package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ameshkov/dnscrypt/v2"
)

var (
	relaysFile    string
	resolverStamp string
	workers       int
	timeout       time.Duration
	checkMode     string
	verbose       bool
	proto         string
	topN          int
	onlyEU	      bool
	recommendN    int
	globalClient       *dnscrypt.Client
	globalResolverInfo *dnscrypt.ResolverInfo

	// ODoH
	odohRelaysFile  string
	odohServersFile string
	odohOutFile     string
	odohTestTarget  string

	// Matrix / auto
	resolversFile      string
	matrixOutFile      string
	resolversFilterOut string
	dnscryptOutFile    string
	outPrefix          string
	// Other
	excludeRelayPatterns string
)

// RelayEntry — an entry from relays.md
type RelayEntry struct {
	Name  string
	Stamp string
}

func init() {
	flag.StringVar(&relaysFile, "f", "/etc/dnscrypt-proxy/relays.md", "path to the relays.md file")
	flag.StringVar(&resolverStamp, "stamp", "sdns://AQMAAAAAAAAAETk0LjE0MC4xNC4xNDo1NDQzINErR_JS3PLCu_iZEIbq95zkSV2LFsigxDIuUso_OQhzIjIuZG5zY3J5cHQuZGVmYXVsdC5uczEuYWRndWFyZC5jb20", "resolver stamp to test with")
	flag.IntVar(&workers, "c", 50, "number of workers")
	flag.DurationVar(&timeout, "timeout", 5*time.Second, "request timeout")
	flag.StringVar(&checkMode, "mode", "tcp", "check mode: tcp | dnscrypt | matrix | resolvers-filter | odoh | auto")
	flag.BoolVar(&verbose, "v", false, "show failed attempts")
	flag.StringVar(&proto, "proto", "udp", "protocol to the relay: udp or tcp")

	flag.StringVar(&odohRelaysFile, "odoh-relays", "odoh-relays.md", "path to odoh-relays.md")
	flag.StringVar(&odohServersFile, "odoh-servers", "odoh-servers.md", "path to odoh-servers.md")
	flag.StringVar(&odohOutFile, "odoh-out", "odoh-results.txt", "where to write ODoH results")
	flag.StringVar(&odohTestTarget, "odoh-target", "odoh-cloudflare", "target name for Stage 1")

	flag.StringVar(&resolversFile, "resolvers", "/etc/dnscrypt-proxy2/public-resolvers.md", "path to public-resolvers.md")
	flag.StringVar(&matrixOutFile, "matrix-out", "matrix.tsv", "where to write the matrix")
	flag.StringVar(&resolversFilterOut, "resolvers-filter-out", "resolvers-alive.md", "where to write live resolvers")
	flag.StringVar(&dnscryptOutFile, "out-file", "", "where to write live relays (only for -mode dnscrypt)")
	flag.IntVar(&topN, "top", 5, "how many resolvers to recommend (for -mode matrix/auto)")
	flag.StringVar(&outPrefix, "out-prefix", "auto", "output file prefix for -mode auto")
	flag.StringVar(&excludeRelayPatterns, "exclude-relay-pattern", "moscow,russia,msk,spb", "substrings in relay names to exclude (comma-separated)")
	flag.BoolVar(&onlyEU, "only-eu", false, "keep only European relays (by name)")
	flag.IntVar(&recommendN, "recommend", 10, "number of resolvers to recommend in final config (for -mode matrix/auto)")
}

func main() {
	flag.Parse()

	switch checkMode {
		case "odoh":
			runODoHPipeline()
			return
		case "matrix":
			runMatrixPipeline(resolversFile, relaysFile, matrixOutFile)
			return
		case "resolvers-filter":
			runResolversFilter(resolversFile, resolversFilterOut, false)
			return
		case "auto":
			runAutoPipeline(resolversFile, relaysFile, outPrefix, topN)
			return
		case "dnscrypt", "tcp":
			// Continue below
		default:
			fmt.Printf("Unknown mode: %s\n", checkMode)
			os.Exit(1)
	}

	// === Only for -mode dnscrypt and -mode tcp ===

	if checkMode == "dnscrypt" && dnscryptOutFile != "" {
		if err := os.WriteFile(dnscryptOutFile, []byte{}, 0644); err != nil {
			fmt.Printf("Error creating file %s: %v\n", dnscryptOutFile, err)
			os.Exit(1)
		}
		fmt.Printf("[*] Live relays will be written to %s\n", dnscryptOutFile)
	}

	file, err := os.Open(relaysFile)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	var relays []RelayEntry
	scanner := bufio.NewScanner(file)
	currentName := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "## ") {
			currentName = strings.TrimPrefix(line, "## ")
			continue
		}
		if strings.HasPrefix(line, "sdns://") {
			if currentName == "" {
				currentName = "unknown"
			}
			relays = append(relays, RelayEntry{Name: currentName, Stamp: line})
			currentName = ""
		}
	}

	if len(relays) == 0 {
		fmt.Println("No relay stamps found in the file.")
		os.Exit(1)
	}

	fmt.Printf("[*] Relays found: %d\n", len(relays))
	fmt.Println("[*] Establishing DNSCrypt session with the resolver...")
	globalClient = &dnscrypt.Client{Net: "udp", Timeout: timeout}
	var dialErr error
	globalResolverInfo, dialErr = globalClient.Dial(resolverStamp)
	if dialErr != nil {
		fmt.Printf("Dial error with resolver: %v\n", dialErr)
		os.Exit(1)
	}
	fmt.Println("[*] Session established")
	fmt.Printf("[*] Check mode: %s\n", checkMode)
	fmt.Println("[*] Starting checks...")

	jobs := make(chan RelayEntry, len(relays))
	results := make(chan string, len(relays))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range jobs {
				addr, err := DecodeRelayStamp(entry.Stamp)
				if err != nil {
					if verbose {
						results <- fmt.Sprintf("[-] %s: %v", entry.Name, err)
					}
					continue
				}

				var ok bool
				var latency time.Duration

				switch checkMode {
					case "tcp":
						ok, latency = checkTCP(addr, timeout)
					case "dnscrypt":
						useTCP := proto == "tcp"
						var err error
						ok, latency, err = checkRelayDNSCrypt(addr, resolverStamp, timeout, useTCP)
						if !ok && verbose && err != nil {
							results <- fmt.Sprintf("[-] %s %s: %v", entry.Name, addr, err)
						}
						if ok && dnscryptOutFile != "" {
							writeAliveRelay(dnscryptOutFile, entry)
						}
				}

				if ok {
					results <- fmt.Sprintf("[+] %s %s is working (%.0f ms)",
							       entry.Name, addr, latency.Seconds()*1000)
				}
			}
		}()
	}

	go func() {
		for _, entry := range relays {
			jobs <- entry
		}
		close(jobs)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		fmt.Println(r)
	}

	fmt.Println("\n[*] Check complete.")
}

// checkTCP checks relay availability via a TCP connect.
func checkTCP(addr string, timeout time.Duration) (bool, time.Duration) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false, time.Since(start)
	}
	conn.Close()
	return true, time.Since(start)
}
