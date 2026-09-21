package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/cloudflare/odoh-go"
	"github.com/miekg/dns"
)

// fetchODoHConfigs fetches the list of ODoH configs from the target over HTTPS.
func fetchODoHConfigs(targetHostname string, timeout time.Duration) (*odoh.ObliviousDoHConfigs, error) {
	url := fmt.Sprintf("https://%s/.well-known/odohconfigs", targetHostname)
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/oblivious-dns-message")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	configs, err := odoh.UnmarshalObliviousDoHConfigs(body)
	if err != nil {
		return nil, fmt.Errorf("parse odoh configs: %w", err)
	}

	return &configs, nil
}

// buildDNSQuery builds a DNS query in wire format (binary).
func buildDNSQuery(name string) ([]byte, error) {
	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(name), dns.TypeA)
	msg.RecursionDesired = true

	packed, err := msg.Pack()
	if err != nil {
		return nil, fmt.Errorf("pack dns msg: %w", err)
	}
	return packed, nil
}

// CheckODoHTargetViaRelay checks that a specific target is reachable
// through a specific relay.
func CheckODoHTargetViaRelay(targetHost, targetPath, relayHost, relayPath string, timeout time.Duration) (bool, time.Duration, error) {
	start := time.Now()

	// 1. Fetch the list of ODoH configs from the target
	configs, err := fetchODoHConfigs(targetHost, timeout)
	if err != nil {
		return false, 0, fmt.Errorf("fetch configs: %w", err)
	}
	if len(configs.Configs) == 0 {
		return false, 0, fmt.Errorf("no odoh configs from target")
	}
	config := configs.Configs[0]

	// 2. Encrypt the DNS query via HPKE
	dnsQuery, err := buildDNSQuery("example.com")
	if err != nil {
		return false, 0, fmt.Errorf("build query: %w", err)
	}

	encryptedMsg, queryContext, err := odoh.SealQuery(dnsQuery, config.Contents)
	if err != nil {
		return false, 0, fmt.Errorf("seal query: %w", err)
	}

	// 3. Serialize the message
	body := encryptedMsg.Marshal()

	// 4. Send a POST to the relay
	relayURL := fmt.Sprintf("https://%s%s?targethost=%s&targetpath=%s",
		relayHost, relayPath,
		url.QueryEscape(targetHost),
		url.QueryEscape(targetPath))
	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest("POST", relayURL, bytes.NewReader(body))
	if err != nil {
		return false, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/oblivious-dns-message")
	req.Header.Set("Accept", "application/oblivious-dns-message")

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return false, 0, fmt.Errorf("relay status %d, body=%q",
			resp.StatusCode, string(errBody))
	}

	// 5. Read the response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, 0, fmt.Errorf("read resp: %w", err)
	}

	// 6. Parse the response as an ObliviousDNSMessage
	respMsg, err := odoh.UnmarshalDNSMessage(respBody)
	if err != nil {
		if len(respBody) > 2 {
			respMsg, err = odoh.UnmarshalDNSMessage(respBody[2:])
		}
		if err != nil {
			return false, 0, fmt.Errorf("unmarshal resp: %w", err)
		}
	}

	// 7. Decrypt
	plain, err := queryContext.DecryptResponse(respMsg)
	if err != nil {
		return false, 0, fmt.Errorf("decrypt: %w", err)
	}

	// 8. Check that this is a valid DNS response.
	// If the first byte isn't the start of a DNS header, try a 2-byte offset
	// (the inner ObliviousDNSResponse wrapper adds a length prefix).
	if tryUnpackDNS(plain) {
		return true, time.Since(start), nil
	}
	if len(plain) > 2 && tryUnpackDNS(plain[2:]) {
		return true, time.Since(start), nil
	}

	headLen := len(plain)
	if headLen > 16 {
		headLen = 16
	}
	return false, 0, fmt.Errorf("unpack dns failed (len=%d, head=%x)",
		len(plain), plain[:headLen])
}

// tryUnpackDNS tries to parse the bytes as a DNS message.
func tryUnpackDNS(b []byte) bool {
	msg := new(dns.Msg)
	return msg.Unpack(b) == nil
}
