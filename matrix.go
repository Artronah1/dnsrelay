package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ameshkov/dnscrypt/v2"
	"github.com/miekg/dns"
)

// MatrixResult — result of a single (resolver, relay) pair check
type MatrixResult struct {
	ResolverName string
	RelayName    string
	OK           bool
	LatencyMs    float64
	Err          string
	ForwardsGoogle bool  // true if the resolver forwards to Google
}

// pair — a "relay + latency" pair.
type pair struct {
	Relay   string
	Latency float64
}

type resStat struct {
	Name  string
	OK    int
	Total int
	Pairs []pair
}

// runMatrixPipeline checks all resolver × relay combinations.
func runMatrixPipeline(resolversFile, relaysFile, outFile string) {
	// 1. Read resolvers
	resolvers, err := readResolversFile(resolversFile)
	if err != nil {
		fmt.Printf("Error reading resolvers: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[*] Resolvers: %d\n", len(resolvers))

	// 2. Read relays (reusing the logic from main.go)
	relays, err := readRelaysFileSimple(relaysFile)
	if err != nil {
		fmt.Printf("Error reading relays: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[*] Relays: %d\n", len(relays))

	totalPairs := len(resolvers) * len(relays)
	fmt.Printf("[*] Total pairs to check: %d\n", totalPairs)
	fmt.Printf("[*] Workers: %d, timeout: %s\n", workers, timeout)
	fmt.Println("[*] Starting checks...")

	// 3. Prepare the job channel
	type pair struct {
		Resolver ResolverEntry
		Relay    RelayEntry
	}
	jobs := make(chan pair, totalPairs)
	results := make(chan MatrixResult, totalPairs)

	// 4. Fill the queue
	go func() {
		for _, r := range resolvers {
			for _, relay := range relays {
				jobs <- pair{Resolver: r, Relay: relay}
			}
		}
		close(jobs)
	}()

	// 5. Start the workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				res := checkResolverViaRelay(p.Resolver, p.Relay)
				results <- res
			}
		}()
	}

	// 6. Close results once all workers are done
	go func() {
		wg.Wait()
		close(results)
	}()

	// 7. Collect all results + print progress + periodic dump
	var all []MatrixResult
	var allMu sync.Mutex
	var done int64
	startTime := time.Now()

	// Dump goroutine — saves the current state to TSV every 30 seconds
	dumpDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
				case <-ticker.C:
					allMu.Lock()
					snapshot := make([]MatrixResult, len(all))
					copy(snapshot, all)
					allMu.Unlock()
					if len(snapshot) > 0 {
						writeMatrixTSV(outFile, resolvers, relays, snapshot)
						fmt.Printf("[*] Dump: %d/%d results saved\n", len(snapshot), totalPairs)
					}
				case <-dumpDone:
					return
			}
		}
	}()

	// Progress goroutine — prints [done/total] every 5 seconds
	progressDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
				case <-ticker.C:
					d := atomic.LoadInt64(&done)
					elapsed := time.Since(startTime).Round(time.Second)
					eta := "?"
					if d > 0 {
						remaining := time.Duration(float64(elapsed) * float64(int64(totalPairs)-d) / float64(d))
						eta = remaining.Round(time.Second).String()
					}
					fmt.Printf("[*] Progress: %d/%d (%.1f%%), elapsed %s, remaining ~%s\n",
						   d, totalPairs, float64(d)*100/float64(totalPairs), elapsed, eta)
				case <-progressDone:
					return
			}
		}
	}()

	for r := range results {
		allMu.Lock()
		all = append(all, r)
		allMu.Unlock()
		atomic.AddInt64(&done, 1)
	}
	close(progressDone)
	close(dumpDone)

	// 8. Write the TSV matrix
	writeMatrixTSV(outFile, resolvers, relays, all)

	// 9. Print a brief summary
	printMatrixSummary(all)

	// 10. Print the ready-made routes and write them to a file
	routesFile := ""
	if outFile != "" {                                            // ← was matrixOutFile
		routesFile = strings.TrimSuffix(outFile, ".tsv") + "-routes.toml"
		if !strings.HasSuffix(routesFile, ".toml") {
			routesFile = outFile + ".routes.toml"
		}
	}
	printSuggestedRoutes(all, routesFile)
}

// checkResolverViaRelay checks a single (resolver, relay) pair with a real query.
func checkResolverViaRelay(resolver ResolverEntry, relay RelayEntry) MatrixResult {
	res := MatrixResult{
		ResolverName: resolver.Name,
		RelayName:    relay.Name,
	}

	start := time.Now()

	// A separate client for each pair, so as not to interfere with other workers
	client := &dnscrypt.Client{
		Net:     "udp",
		Timeout: timeout,
	}

	ri, err := client.Dial(resolver.Stamp)
	if err != nil {
		res.Err = fmt.Sprintf("dial: %v", err)
		res.LatencyMs = float64(time.Since(start).Milliseconds())
		return res
	}

	// Prepare the anon header for the target resolver
	relayAddr, err := DecodeRelayStamp(relay.Stamp)
	if err != nil {
		res.Err = fmt.Sprintf("relay stamp: %v", err)
		return res
	}

	// Open a connection to the relay
	conn, err := net.DialTimeout("udp", relayAddr, timeout)
	if err != nil {
		res.Err = fmt.Sprintf("dial relay: %v", err)
		res.LatencyMs = float64(time.Since(start).Milliseconds())
		return res
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	// Wrap in anonConn
	host, portStr, _ := net.SplitHostPort(ri.ServerAddress)
	ip := net.ParseIP(host)
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	anon := &anonConn{
		Conn:         conn,
		resolverIP:   ip,
		resolverPort: port,
		useTCP:       false,
	}

	// Send a test query
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	req.RecursionDesired = true

	resp, err := client.ExchangeConn(anon, req, ri)
	if err != nil {
		res.Err = fmt.Sprintf("exchange: %v", err)
		res.LatencyMs = float64(time.Since(start).Milliseconds())
		return res
	}
	if resp == nil || len(resp.Answer) == 0 {
		res.Err = "empty answer"
		res.LatencyMs = float64(time.Since(start).Milliseconds())
		return res
	}

	res.OK = true
	res.LatencyMs = float64(time.Since(start).Milliseconds())

	// Check for forwarding to Google via whoami.akamai.net
	googleCheck := checkWhoami(anon, client, ri, timeout)
	res.ForwardsGoogle = googleCheck
	return res
}

// writeMatrixTSV writes results to a TSV file: rows are resolvers, columns are relays.
func writeMatrixTSV(path string, resolvers []ResolverEntry, relays []RelayEntry, results []MatrixResult) {
	out, err := os.Create(path)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return
	}
	defer out.Close()

	w := bufio.NewWriter(out)
	defer w.Flush()

	// Header
	w.WriteString("resolver\\relay")
	for _, r := range relays {
		w.WriteString("\t" + r.Name)
	}
	w.WriteString("\n")

	// Results index for fast lookup
	idx := make(map[string]map[string]MatrixResult)
	for _, r := range results {
		if idx[r.ResolverName] == nil {
			idx[r.ResolverName] = make(map[string]MatrixResult)
		}
		idx[r.ResolverName][r.RelayName] = r
	}

	// Rows
	for _, r := range resolvers {
		w.WriteString(r.Name)
		for _, relay := range relays {
			w.WriteString("\t")
			if res, ok := idx[r.Name][relay.Name]; ok {
				if res.OK {
					w.WriteString(fmt.Sprintf("%.0f", res.LatencyMs))
				} else {
					w.WriteString("X")
				}
			} else {
				w.WriteString("-")
			}
		}
		w.WriteString("\n")
	}

	fmt.Printf("[*] Matrix written to %s\n", path)
}

// printMatrixSummary prints a brief summary.
func printMatrixSummary(results []MatrixResult) {
	type stat struct {
		ResolverName string
		OK           int
		Total        int
		MinLatency   float64
		BestRelay    string
	}
	stats := make(map[string]*stat)

	for _, r := range results {
		// Skip empty names and junk
		if r.ResolverName == "" {
			continue
		}
		s, ok := stats[r.ResolverName]
		if !ok {
			s = &stat{ResolverName: r.ResolverName}
			stats[r.ResolverName] = s
		}
		s.Total++
		if r.OK {
			s.OK++
			if s.MinLatency == 0 || r.LatencyMs < s.MinLatency {
				s.MinLatency = r.LatencyMs
				s.BestRelay = r.RelayName
			}
		}
	}

	// Sort by number of OK results
	var list []*stat
	for _, s := range stats {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].OK > list[j].OK })

	fmt.Println("\n=== Resolver summary ===")
	fmt.Printf("%-45s %8s %8s  %s\n", "resolver", "OK/total", "min_ms", "best_relay")
	for _, s := range list {
		fmt.Printf("%-45s %3d/%-4d %8.0f  %s\n",
			   s.ResolverName, s.OK, s.Total, s.MinLatency, s.BestRelay)
	}
}

// printSuggestedRoutes prints a ready-made routes block for the toml, with filtering.
// If outFile != "" — writes the result to a file.
func printSuggestedRoutes(results []MatrixResult, outFile string) {
	// 1. Compute the health of each resolver
	byResolver := make(map[string]*resStat)
	for _, r := range results {
		if r.ResolverName == "" {
			continue
		}
		s, ok := byResolver[r.ResolverName]
		if !ok {
			s = &resStat{Name: r.ResolverName}
			byResolver[r.ResolverName] = s
		}
		// Filter by relay name (before Total++)
		if isRelayExcluded(r.RelayName) {
			continue
		}
		s.Total++
		if r.OK {
			s.OK++
			s.Pairs = append(s.Pairs, pair{Relay: r.RelayName, Latency: r.LatencyMs})
		}
	}

		// 2. Filter: health > 50%, not in the blacklist
	blacklistPatterns := []string{
		"dct-",               // forward to Google
		"cisco",              // fragmentation issues
		"cleanbrowsing",
		"comodo",
		"adguard-dns-family", // non-standard certificate + filtering
		"adguard-dns",        // filters ads
	}

	// Count how many times each resolver forwarded to Google
	googleForwardCount := make(map[string]int)
	for _, r := range results {
		if r.ForwardsGoogle {
			googleForwardCount[r.ResolverName]++
		}
	}

	var candidates []*resStat
	for _, s := range byResolver {
		// health > 50%
		if s.Total == 0 || float64(s.OK)/float64(s.Total) < 0.5 {
			continue
		}
		// Google forwarding: if more than half of the pairs go to Google, skip
		if g := googleForwardCount[s.Name]; g > 0 && float64(g)/float64(s.Total) > 0.5 {
			continue
		}
		// not in the blacklist
		skip := false
		for _, p := range blacklistPatterns {
			if strings.Contains(s.Name, p) {
				skip = true
				break
			}
		}
		if skip || s.Name == "" {
			continue
		}
		candidates = append(candidates, s)
	}

	// 3. Sort by health (more OK is better), ties broken by average latency
	sort.Slice(candidates, func(i, j int) bool {
		hi := float64(candidates[i].OK) / float64(candidates[i].Total)
		hj := float64(candidates[j].OK) / float64(candidates[j].Total)
		if hi != hj {
			return hi > hj
		}
		ai := avgLatency(candidates[i].Pairs)
		aj := avgLatency(candidates[j].Pairs)
		return ai < aj
	})

	// 4. Select the top N with relay deduplication
	relayUsage := make(map[string]int)
	maxRelayUsage := 3

	var chosen []*resStat
	for _, s := range candidates {
		if len(chosen) >= recommendN {
			break
		}
		// Pick the top 3 relays with a penalty for reuse
		type scoredPair struct {
			Pair  pair
			Score float64
		}
		var scored []scoredPair
		for _, p := range s.Pairs {
			penalty := float64(relayUsage[p.Relay]) * 50.0
			scored = append(scored, scoredPair{Pair: p, Score: p.Latency + penalty})
		}
		sort.Slice(scored, func(i, j int) bool {
			return scored[i].Score < scored[j].Score
		})

		var relays []string
		usedInThisResolver := make(map[string]bool)
		for _, sp := range scored {
			if len(relays) >= 3 {
				break
			}
			if usedInThisResolver[sp.Pair.Relay] {
				continue
			}
			if relayUsage[sp.Pair.Relay] >= maxRelayUsage {
				continue
			}
			relays = append(relays, sp.Pair.Relay)
			relayUsage[sp.Pair.Relay]++
			usedInThisResolver[sp.Pair.Relay] = true
		}
		if len(relays) < 2 {
			continue
		}
		// Overwrite pairs with the selected ones
		newPairs := make([]pair, 0, len(relays))
		for _, r := range relays {
			for _, p := range s.Pairs {
				if p.Relay == r {
					newPairs = append(newPairs, p)
					break
				}
			}
		}
		s.Pairs = newPairs
		chosen = append(chosen, s)
	}

	// 5. Print the final block
	fmt.Printf("\n=== Recommended %d resolvers for dnscrypt-proxy.toml ===\n", len(chosen))
	fmt.Println()
	fmt.Println("server_names = [")
	for _, s := range chosen {
		fmt.Printf("    '%s',\n", s.Name)
	}
	fmt.Println("]")
	fmt.Println()
	fmt.Println("[anonymized_dns]")
	fmt.Println("routes = [")
	for _, s := range chosen {
		var relayList []string
		var latencyList []string
		for _, p := range s.Pairs {
			relayList = append(relayList, "'"+p.Relay+"'")
			latencyList = append(latencyList, fmt.Sprintf("%.0f", p.Latency))
		}
		health := float64(s.OK) / float64(s.Total) * 100
		fmt.Printf("    { server_name = '%s', via = [%s] },  # health %.0f%%, latencies: %s ms\n",
			   s.Name, strings.Join(relayList, ", "), health, strings.Join(latencyList, ", "))
	}
	fmt.Println("]")
	fmt.Println()
	fmt.Println("[*] Use these lines in /etc/dnscrypt-proxy2/dnscrypt-proxy.toml")
	fmt.Println("[*] Then restart: /etc/init.d/dnscrypt-proxy restart")

	// 6. Write to a file, if specified
	if outFile != "" {
		if err := writeRoutesFile(outFile, chosen); err != nil {
			fmt.Printf("[!] Error writing %s: %v\n", outFile, err)
		} else {
			fmt.Printf("[*] Config written to %s\n", outFile)
		}
	}
}

// writeRoutesFile writes the ready-made routes block to a TOML file.
func writeRoutesFile(path string, chosen []*resStat) error {
	var buf strings.Builder

	buf.WriteString("# Automatically generated by dnsrelay\n")
	buf.WriteString("# Date: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")

	buf.WriteString("server_names = [\n")
	for _, s := range chosen {
		buf.WriteString(fmt.Sprintf("    '%s',\n", s.Name))
	}
	buf.WriteString("]\n\n")

	buf.WriteString("[anonymized_dns]\n")
	buf.WriteString("routes = [\n")
	for _, s := range chosen {
		var relayList []string
		var latencyList []string
		for _, p := range s.Pairs {
			relayList = append(relayList, "'"+p.Relay+"'")
			latencyList = append(latencyList, fmt.Sprintf("%.0f", p.Latency))
		}
		health := float64(s.OK) / float64(s.Total) * 100
		buf.WriteString(fmt.Sprintf(
			"    { server_name = '%s', via = [%s] },  # health %.0f%%, latencies: %s ms\n",
			s.Name, strings.Join(relayList, ", "), health, strings.Join(latencyList, ", ")))
	}
	buf.WriteString("]\n")

	return os.WriteFile(path, []byte(buf.String()), 0644)
}

// avgLatency computes the average latency across a list of pairs.
func avgLatency(pairs []pair) float64 {
	if len(pairs) == 0 {
		return 0
	}
	var sum float64
	for _, p := range pairs {
		sum += p.Latency
	}
	return sum / float64(len(pairs))
}

// runAutoPipeline — full pipeline: live resolvers → live relays → matrix → ready-made config.
func runAutoPipeline(resolversFile, relaysFile, outPrefix string, topN int) {
	start := time.Now()
	fmt.Println("=========================================================")
	fmt.Println("[*] AUTO PIPELINE — full pipeline")
	fmt.Println("=========================================================")

	// ---- STEP 1: live resolvers ----
	fmt.Println("\n=== STEP 1/3: filtering live resolvers ===")
	resolversAlive := outPrefix + "-resolvers-alive.md"
	runResolversFilter(resolversFile, resolversAlive, false)

	// ---- STEP 2: live relays ----
	fmt.Println("\n=== STEP 2/3: filtering live relays ===")
	relaysAlive := outPrefix + "-relays-alive.md"
	runRelaysFilter(relaysFile, relaysAlive)

	// ---- STEP 3: matrix for the top N ----
	fmt.Println("\n=== STEP 3/3: building the matrix and recommending resolvers ===")
	topResolversFile := outPrefix + "-resolvers-top.md"
	topRelaysFile := outPrefix + "-relays-top.md"

	// Limit to the top N (3 lines per entry: ##, sdns, blank)
	if err := headFile(resolversAlive, topResolversFile, topN*3); err != nil {
		fmt.Printf("Error in head %s: %v\n", resolversAlive, err)
		return
	}
	if err := headFile(relaysAlive, topRelaysFile, topN*3); err != nil {
		fmt.Printf("Error in head %s: %v\n", relaysAlive, err)
		return
	}

	matrixOut := outPrefix + "-matrix.tsv"
	runMatrixPipeline(topResolversFile, topRelaysFile, matrixOut)

	elapsed := time.Since(start)
	fmt.Printf("\n[*] AUTO PIPELINE finished in %s\n", elapsed)
	routesOut := strings.TrimSuffix(matrixOut, ".tsv") + "-routes.toml"
	fmt.Printf("[*] Files: %s, %s, %s, %s\n",
		   resolversAlive, relaysAlive, matrixOut, routesOut)
}

// headFile copies the first n lines from src to dst.
func headFile(src, dst string, n int) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	scanner := bufio.NewScanner(in)
	count := 0
	w := bufio.NewWriter(out)
	defer w.Flush()

	for scanner.Scan() && count < n {
		w.WriteString(scanner.Text() + "\n")
		count++
	}

	return scanner.Err()
}

// excludePatternsCache — cache of parsed patterns (so they are not re-parsed every time).
var excludePatternsCache []string

// isRelayExcluded checks whether a relay name matches an exclude pattern
// or fails the -only-eu filter.
func isRelayExcluded(relayName string) bool {
	lower := strings.ToLower(relayName)

	// "EU only" filter (whitelist)
	if onlyEU {
		if !isEURelay(lower) {
			return true
		}
	}

	// Regular exclude filter (by substrings)
	if len(excludePatternsCache) == 0 {
		if excludeRelayPatterns == "" {
			return false
		}
		for _, p := range strings.Split(excludeRelayPatterns, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				excludePatternsCache = append(excludePatternsCache, strings.ToLower(p))
			}
		}
	}
	for _, p := range excludePatternsCache {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// checkWhoami queries whoami.akamai.net through the same relay and checks
// whether the responding IP belongs to a Google subnet.
func checkWhoami(anon *anonConn, client *dnscrypt.Client, ri *dnscrypt.ResolverInfo, timeout time.Duration) bool {
	req := new(dns.Msg)
	req.SetQuestion("whoami.akamai.net.", dns.TypeA)
	req.RecursionDesired = true

	resp, err := client.ExchangeConn(anon, req, ri)
	if err != nil || resp == nil || len(resp.Answer) == 0 {
		return false
	}

	for _, ans := range resp.Answer {
		if a, ok := ans.(*dns.A); ok {
			if isGoogleIP(a.A) {
				return true
			}
		}
	}
	return false
}

// isGoogleIP checks whether the IP belongs to known Google DNS subnets.
func isGoogleIP(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	// Known Google DNS subnets (whoami will return the resolver's IP)
	googlePrefixes := []string{
		"8.8.8.", "8.8.4.",
		"172.253.", "172.217.",
		"74.125.", "173.194.",
		"192.178.", "142.250.", "142.251.",
		"64.233.", "66.102.", "66.249.",
		"209.85.", "216.58.", "216.239.",
	}
	ipStr := ip4.String()
	for _, p := range googlePrefixes {
		if strings.HasPrefix(ipStr, p) {
			return true
		}
	}
	return false
}

// isEURelay checks by name whether a relay is European.
// Matched against whitelist patterns.
func isEURelay(name string) bool {
	euPatterns := []string{
		// Germany
		"cs-de", "cs-berlin", "cs-dus", "cs-frankfurt",
		// Netherlands
		"cs-nl", "cs-amsterdam",
		// France
		"cs-fr", "cs-paris", "cs-marseille",
		// Italy
		"cs-milan", "cs-rome",
		// Spain
		"cs-barcelona", "cs-madrid",
		// UK (post-Brexit, but still Europe)
		"cs-london", "cs-manchester", "cs-newcastle", "cs-redditch", "cs-coventry",
		// Austria, Switzerland
		"cs-austria", "cs-ch", "cs-zurich", "cs-geneva",
		// Czechia, Hungary, Poland, Romania, Serbia, Bulgaria
		"cs-czech", "cs-prague", "cs-hungary", "cs-poland", "cs-warsaw", "cs-bucharest", "cs-ro", "cs-serbia", "cs-sofia", "cs-bratislava",
		// Scandinavia
		"cs-finland", "cs-swe", "cs-norway", "cs-stockholm", "cs-copenhagen", "cs-helsinki", "cs-oslo",
		// Baltics
		"cs-tallinn", "cs-riga", "cs-vilnius",
		// Portugal, Greece
		"cs-pt", "cs-lisbon", "cs-thessaloniki", "cs-athens",
		// Moldova (close to the EU, usually kept as well)
		"cs-md", "cs-chisinau",
		// dnscry.pt — a European operator, but locations vary, so keep only European cities
		"dnscry.pt-anon-amsterdam", "dnscry.pt-anon-bremen", "dnscry.pt-anon-copenhagen",
		"dnscry.pt-anon-dublin", "dnscry.pt-anon-dusseldorf", "dnscry.pt-anon-eygelshoven",
		"dnscry.pt-anon-frankfurt", "dnscry.pt-anon-gdansk", "dnscry.pt-anon-geneva",
		"dnscry.pt-anon-hafnarfjordur", "dnscry.pt-anon-helsinki", "dnscry.pt-anon-hudiksvall",
		"dnscry.pt-anon-lisbon", "dnscry.pt-anon-london", "dnscry.pt-anon-madrid",
		"dnscry.pt-anon-molln", "dnscry.pt-anon-munich", "dnscry.pt-anon-naaldwijk",
		"dnscry.pt-anon-newcastle", "dnscry.pt-anon-nuremberg", "dnscry.pt-anon-paris",
		"dnscry.pt-anon-prague", "dnscry.pt-anon-redditch", "dnscry.pt-anon-riga",
		"dnscry.pt-anon-sandefjord", "dnscry.pt-anon-sofia", "dnscry.pt-anon-stockholm",
		"dnscry.pt-anon-tallinn", "dnscry.pt-anon-thessaloniki", "dnscry.pt-anon-timisoara",
		"dnscry.pt-anon-tirana", "dnscry.pt-anon-tuusula", "dnscry.pt-anon-vienna",
		"dnscry.pt-anon-vilnius", "dnscry.pt-anon-warsaw", "dnscry.pt-anon-zurich",
		// Scaleway (France)
		"anon-scaleway", "anon-scaleway-ams", "anon-scaleway2",
		// Other EU
		"anon-kama", "anon-tiarap", "anon-serbica", "anon-dnswarden-swiss",
	}
	for _, p := range euPatterns {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}
