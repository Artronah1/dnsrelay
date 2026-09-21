package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"

	"github.com/miekg/dns"
)

// anonConn — wrapper around a UDP/TCP connection to a relay.
// Adds the DNSCrypt anonymization header on every write.
type anonConn struct {
	net.Conn
	resolverIP   net.IP
	resolverPort uint16
	useTCP       bool
}

// Write intercepts the encrypted packet and prepends the anon header.
func (c *anonConn) Write(b []byte) (int, error) {
	header := make([]byte, 0, 28)

	for i := 0; i < 8; i++ {
		header = append(header, 0xff)
	}
	header = append(header, 0x00, 0x00)

	if ip4 := c.resolverIP.To4(); ip4 != nil {
		header = append(header,
				0x00, 0x00, 0x00, 0x00,
		  0x00, 0x00, 0x00, 0x00,
		  0x00, 0x00, 0xff, 0xff,
		)
		header = append(header, ip4...)
	} else {
		header = append(header, c.resolverIP.To16()...)
	}

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, c.resolverPort)
	header = append(header, portBytes...)

	full := append(header, b...)

	if c.useTCP {
		lengthPrefix := make([]byte, 2)
		binary.BigEndian.PutUint16(lengthPrefix, uint16(len(full)))
		full = append(lengthPrefix, full...)
	}

	n, err := c.Conn.Write(full)
	if err != nil {
		return 0, err
	}

	overhead := len(full) - len(b)
	if n < overhead {
		return 0, fmt.Errorf("short write")
	}
	return n - overhead, nil
}

// checkRelayDNSCrypt — real relay check using an anonymized query.
func checkRelayDNSCrypt(relayAddr, resolverStamp string, timeout time.Duration, useTCP bool) (bool, time.Duration, error) {
	start := time.Now()

	var err error

	netProto := "udp"
	if useTCP {
		netProto = "tcp"
	}

	client := globalClient
	ri := globalResolverInfo

	host, portStr, err := net.SplitHostPort(ri.ServerAddress)
	if err != nil {
		return false, 0, fmt.Errorf("split resolver addr: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false, 0, fmt.Errorf("bad resolver ip: %s", host)
	}
	var port uint16
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return false, 0, fmt.Errorf("bad resolver port: %s", portStr)
	}

	rawConn, err := net.DialTimeout(netProto, relayAddr, timeout)
	if err != nil {
		return false, 0, fmt.Errorf("%s dial relay: %w", netProto, err)
	}
	defer rawConn.Close()
	rawConn.SetDeadline(time.Now().Add(timeout))

	conn := &anonConn{
		Conn:         rawConn,
		resolverIP:   ip,
		resolverPort: port,
		useTCP:       useTCP,
	}

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	req.RecursionDesired = true

	resp, err := client.ExchangeConn(conn, req, ri)
	if err != nil {
		return false, 0, fmt.Errorf("exchange: %w", err)
	}
	if resp == nil || len(resp.Answer) == 0 {
		return false, 0, fmt.Errorf("empty answer")
	}

	return true, time.Since(start), nil
}
