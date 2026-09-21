# dnsrelay

> Find working **(resolver, relay)** pairs for [Anonymized DNSCrypt](https://github.com/DNSCrypt/dnscrypt-proxy/wiki/Anonymized-DNS) and ODoH, and generate a ready-to-use `dnscrypt-proxy.toml` config.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8.svg)](https://go.dev/)
[![Platform](https://img.shields.io/badge/platform-linux-lightgrey.svg)]()

`dnscrypt-proxy` supports Anonymized DNSCrypt via relays, but **not every (resolver, relay) pair works**: some relays block "their own" resolvers, some resolvers forward to Google, some relays are only reachable from certain regions. Finding working pairs manually takes hours.

`dnsrelay` builds a full **resolver × relay matrix** in minutes and outputs a ready-to-use config.

---

## Features

- **7 modes** — `auto`, `dnscrypt`, `resolvers-filter`, `matrix`, `tcp`, `odoh`
- **Full pipeline** — one command from raw `.md` lists to `*-routes.toml`
- **Real-time progress** — with ETA, survives `Ctrl+C` (incremental dump)
- **Auto-detect Google forwarders** — via `whoami.akamai.net` (this catches `dct-*` and similar)
- **Relay deduplication** — max 3 resolvers per relay
- **Region filters** — `-only-eu`, `-exclude-relay-pattern`
- **Multi-arch** — x86_64, ARM64, ARMv7, MIPS (for OpenWrt routers)

---

## Install

### From source

```bash
git clone https://github.com/Artronah1/dnsrelay.git
cd dnsrelay
go build -ldflags="-s -w" -o dnsrelay
```

### Cross-compile

```bash
# ARM64 (Routerich, modern routers)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dnsrelay-arm64

# ARMv7 (Mikrotik, older routers)
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o dnsrelay-armv7

# MIPS big-endian (old TP-Link, Ubiquiti)
CGO_ENABLED=0 GOOS=linux GOARCH=mips GOMIPS=softfloat go build -ldflags="-s -w" -o dnsrelay-mips

# MIPS little-endian (Atheros)
CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w" -o dnsrelay-mipsle
```

---

## Data files

Download once into the same directory:

```bash
# Resolvers
wget https://raw.githubusercontent.com/DNSCrypt/dnscrypt-resolvers/master/v3/public-resolvers.md -O public-resolvers.md

# Relays
wget https://raw.githubusercontent.com/DNSCrypt/dnscrypt-resolvers/master/v3/relays.md -O relays.md

# dnscry.pt (extra relays + resolvers)
wget https://www.dnscry.pt/resolvers.md -O dnscry.pt-resolvers.md

# Quad9 (separate list)
wget https://raw.githubusercontent.com/Quad9DNS/dnscrypt-settings/main/dnscrypt/quad9-resolvers.md -O quad9-resolvers.md

# Optional: merge relays
cat relays.md dnscry.pt-resolvers.md > relays-all.md
```

---

## Usage

### `auto` — full pipeline (recommended)

```bash
./dnsrelay -mode auto \
  -resolvers public-resolvers.md \
  -f relays.md \
  -c 50 \
  -timeout 3s \
  -top 100 \
  -recommend 10 \
  -out-prefix myconfig \
  -only-eu \
  -exclude-relay-pattern "moscow,russia,msk,spb" \
  2>&1 | tee myconfig.log
```

**What it does:**

1. Filters alive resolvers → `myconfig-resolvers-alive.md`
2. Filters alive relays → `myconfig-relays-alive.md`
3. Takes top-100 of each
4. Builds 100×100 matrix → `myconfig-matrix.tsv`
5. Prints and saves the recommended config → `myconfig-matrix-routes.toml`

**Time:** ~20 minutes on a PC, ~40 minutes on a router.

### `dnscrypt` — filter alive relays only

```bash
./dnsrelay -mode dnscrypt \
  -f relays-all.md \
  -c 50 -timeout 5s -proto udp \
  -out-file relays-alive.md
```

### `resolvers-filter` — filter alive resolvers only

```bash
./dnsrelay -mode resolvers-filter \
  -resolvers public-resolvers.md \
  -c 50 -timeout 5s \
  -resolvers-filter-out resolvers-alive.md
```

### `matrix` — build matrix and recommend

```bash
./dnsrelay -mode matrix \
  -resolvers resolvers-alive.md \
  -f relays-alive.md \
  -c 50 -timeout 3s \
  -matrix-out matrix.tsv \
  -recommend 10
```

### `odoh` — check ODoH relays and targets

```bash
./dnsrelay -mode odoh -c 10 -timeout 10s
```

---

## Flags

| Flag | Default | Description |
|---|---|---|
| `-mode` | `tcp` | `auto`, `dnscrypt`, `resolvers-filter`, `matrix`, `tcp`, `odoh` |
| `-f` | `/etc/dnscrypt-proxy/relays.md` | Relays file |
| `-resolvers` | `public-resolvers.md` | Resolvers file |
| `-c` | `50` | Workers |
| `-timeout` | `5s` | Request timeout |
| `-proto` | `udp` | Protocol to relay: `udp` or `tcp` |
| `-top` | `100` | Number of resolvers to test in the matrix |
| `-recommend` | `10` | Number of resolvers to recommend in the final config |
| `-out-prefix` | `auto` | Output prefix for `-mode auto` |
| `-matrix-out` | `matrix.tsv` | Where to write the matrix |
| `-out-file` | — | Where to write alive relays (`-mode dnscrypt`) |
| `-resolvers-filter-out` | `resolvers-alive.md` | Where to write alive resolvers |
| `-exclude-relay-pattern` | `moscow,russia,msk,spb` | Substrings to exclude (comma-separated) |
| `-only-eu` | `false` | Only European relays |
| `-v` | `false` | Verbose |
| `-stamp` | AdGuard DNS | Resolver stamp for relay testing |
| `-odoh-relays` | `odoh-relays.md` | ODoH relays file |
| `-odoh-servers` | `odoh-servers.md` | ODoH targets file |
| `-odoh-out` | `odoh-results.txt` | ODoH output |
| `-odoh-target` | `odoh-cloudflare` | Target ODoH server for relay testing |

---

## Output files

### `*-routes.toml`

Ready-to-paste block for `/etc/dnscrypt-proxy/dnscrypt-proxy.toml`:

```toml
# Generated by dnsrelay at 2026-09-21 12:00:00

server_names = [
    'cs-czech',
    'cs-belgium',
    'cs-manchester',
]

[anonymized_dns]
routes = [
    { server_name = 'cs-czech', via = ['anon-scaleway-ams', 'anon-cs-nl', 'dnscry.pt-anon-gdansk-ipv4'] },  # health 98%, latencies: 535, 586, 755 ms
    ...
]
```

- `health` — percentage of working pairs for this resolver.
- `latencies` — measured latencies for the chosen relays.

### `*-matrix.tsv`

Table:
- **Rows** — resolvers
- **Columns** — relays
- **Values**: latency in ms / `X` (timeout) / `-` (not tested)

### `*-resolvers-alive.md` / `*-relays-alive.md`

Same format as the original `.md` files — can be reused as input.

---

## How it works

### Anonymized DNSCrypt packet format

```
[0xff × 8] [0x00 0x00] [IPv6-mapped resolver IP (16 bytes)] [port (2 bytes BE)] [encrypted payload]
```

28-byte header prepended to the encrypted DNSCrypt payload.

The relay sees only the client's IP and the target resolver's IP:port, but cannot read the payload. The resolver sees the decrypted query but not the client's IP.

### Why matrix

Not every pair works:

- Relay may block "own to own" connections (`cs-de` via `anon-cs-de`).
- Resolver may forward to Google (`dct-*`).
- Relay may be too far (500+ ms).
- Resolver may require `direct_cert_fallback = true` (AdGuard).

`dnsrelay` tests all pairs and keeps only working ones.

### Google-forward detection

Query `whoami.akamai.net` via the resolver. Akamai returns the **IP of the resolver that performed the query on your behalf**. If this IP is in a Google subnet (`8.8.8.8`, `172.253.*`, `74.125.*`, `173.194.*`, etc.) — the resolver forwards to Google, and it gets excluded.

This automatically filters out `dct-*` and similar resolvers — no more manual `ipleak.net` checks.

---

## Deployment on OpenWrt router

### Build for ARM64

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -ldflags="-s -w" -o dnsrelay-arm64
```

### Copy to router

```bash
scp dnsrelay-arm64 public-resolvers.md relays.md dnscry.pt-resolvers.md \
  root@192.168.1.1:/tmp/
```

### Run on router

```bash
ssh root@192.168.1.1
cd /tmp
chmod +x dnsrelay-arm64

./dnsrelay-arm64 -mode auto \
  -resolvers public-resolvers.md \
  -f relays.md \
  -c 5 \
  -timeout 10s \
  -top 50 \
  -recommend 10 \
  -out-prefix router \
  -only-eu \
  2>&1 | tee router-auto.log
```

**Router-specific notes:**

- `-c 5` — keep workers low (limited RAM)
- `-timeout 10s` — VPN path is longer
- `-top 50` — 50×50 = 2500 pairs (~30 min)

### Apply the result

```bash
# Copy server_names and routes into:
vi /etc/dnscrypt-proxy2/dnscrypt-proxy.toml

# Check TOML
python3 -c "import tomllib; tomllib.load(open('/etc/dnscrypt-proxy2/dnscrypt-proxy.toml','rb'))" && echo "TOML OK"

# Restart
/etc/init.d/dnscrypt-proxy restart
sleep 10
logread | grep -i "live servers" | tail -1
```

---

## Troubleshooting

| Problem | Fix |
|---|---|
| `too many open files` | Lower `-c` to 20–30 |
| All pairs TIMEOUT | Increase `-timeout` to 10s; check UDP/443 is not blocked |
| `live servers: 0` | Verify `public-resolvers.md`, check `journalctl -u dnscrypt-proxy` |
| `FATAL: toml: line X` | Broken config: unclosed bracket or extra comma in `routes` |
| Misaligned columns in TSV | Cosmetic bug; TSV is for reading, not parsing |

---

## Dependencies

- [github.com/ameshkov/dnscrypt/v2](https://github.com/ameshkov/dnscrypt) — DNSCrypt client
- [github.com/miekg/dns](https://github.com/miekg/dns) — DNS messages
- [github.com/cloudflare/odoh-go](https://github.com/cloudflare/odoh-go) — ODoH

## License

MIT — see [LICENSE](LICENSE).
