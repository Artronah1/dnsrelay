package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
)

func DecodeRelayStamp(stamp string) (string, error) {
	stamp = strings.TrimPrefix(stamp, "sdns://")

	raw, err := base64.RawURLEncoding.DecodeString(stamp)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	if len(raw) < 2 {
		return "", fmt.Errorf("stamp too short")
	}

	if raw[0] != 0x81 {
		return "", fmt.Errorf("not a relay stamp (expected 0x81, got 0x%02x)", raw[0])
	}

	addrLen := int(raw[1])
	if len(raw) < 2+addrLen {
		return "", fmt.Errorf("invalid address length")
	}

	addrStr := string(raw[2 : 2+addrLen])

	if _, _, err := net.SplitHostPort(addrStr); err == nil {
		host, _, _ := net.SplitHostPort(addrStr)
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return "", fmt.Errorf("ipv6 relay skipped")
		}
		return addrStr, nil
	}

	if ip := net.ParseIP(addrStr); ip != nil {
		if ip.To4() == nil {
			return "", fmt.Errorf("ipv6 relay skipped")
		}
		return net.JoinHostPort(addrStr, "443"), nil
	}

	return "", fmt.Errorf("could not extract address from stamp")
}
