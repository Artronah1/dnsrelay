package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
)

// ResolverEntry — an entry from public-resolvers.md
type ResolverEntry struct {
	Name  string // resolver name (quad9-dnscrypt-ip4-nofilter-pri)
	Stamp string // sdns://...
	Addr  string // ip:port, if it could be extracted
}

// readResolversFile reads public-resolvers.md and returns a list of resolvers.
// Format: ## name + sdns://...
func readResolversFile(path string) ([]ResolverEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []ResolverEntry
	scanner := bufio.NewScanner(file)
	currentName := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "## ") {
			currentName = strings.TrimPrefix(line, "## ")
			continue
		}

		if strings.HasPrefix(line, "sdns://") {
			entry := ResolverEntry{
				Name:  currentName,
				Stamp: line,
			}
			// Try to extract the address (not critical if it fails)
			if addr, err := decodeResolverStampAddr(line); err == nil {
				entry.Addr = addr
			}
			out = append(out, entry)
			currentName = ""
		}
	}

	return out, scanner.Err()
}

// decodeResolverStampAddr extracts ip:port from a DNSCrypt resolver stamp (0x01).
// The format differs from relays: [0x01][props 8][LP addr]...
func decodeResolverStampAddr(stamp string) (string, error) {
	stamp = strings.TrimPrefix(stamp, "sdns://")
	raw, err := base64.RawURLEncoding.DecodeString(stamp)
	if err != nil {
		return "", err
	}
	if len(raw) < 2 || raw[0] != 0x01 {
		return "", fmt.Errorf("not a resolver stamp")
	}
	// After props (8 bytes) comes the LP address, starting at offset 9
	offset := 9
	if len(raw) < offset+1 {
		return "", fmt.Errorf("too short")
	}
	addrLen := int(raw[offset])
	offset++
	if len(raw) < offset+addrLen {
		return "", fmt.Errorf("addr length overflow")
	}
	addr := string(raw[offset : offset+addrLen])
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", err
	}
	return addr, nil
}
