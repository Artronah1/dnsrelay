package main

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
)

type ODoHStamp struct {
	Kind     string // "relay" or "target"
	Hostname string // e.g. "odoh-relay.edgecompute.app"
	Path     string // e.g. "/" or "/dns-query"
}

// asciiRe finds runs of printable ASCII characters of length >= 3.
var asciiRe = regexp.MustCompile(`[ -~]{3,}`)

func ParseODoHStamp(stamp string) (*ODoHStamp, error) {
	stamp = strings.TrimPrefix(stamp, "sdns://")

	raw, err := base64.RawURLEncoding.DecodeString(stamp)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	if len(raw) < 1 {
		return nil, fmt.Errorf("stamp too short")
	}

	var kind string
	switch raw[0] {
		case 0x05:
			kind = "target"
		case 0x85:
			kind = "relay"
		default:
			return nil, fmt.Errorf("not an ODoH stamp (got 0x%02x)", raw[0])
	}

	// Extract all ASCII chunks from the stamp
	chunks := asciiRe.FindAllString(string(raw), -1)

	var hostname, path string
	for _, c := range chunks {
		// Skip junk (only letters/digits/dots/colons/hyphens)
		if strings.HasPrefix(c, "/") {
			if path == "" {
				path = c
			}
			continue
		}
		// hostname with or without a port
		if strings.Contains(c, ".") && !strings.Contains(c, " ") && hostname == "" {
			hostname = c
		}
	}

	if hostname == "" {
		return nil, fmt.Errorf("hostname not found in stamp")
	}

	if path == "" {
		path = "/dns-query" // default for targets if none was found
	}

	return &ODoHStamp{
		Kind:     kind,
		Hostname: hostname,
		Path:     path,
	}, nil
}
