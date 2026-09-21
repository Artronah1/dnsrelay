# README.md для `dnsrelay`

Скопируй в `~/dnsbrute/README.md`:

```markdown
# dnsrelay

Утилита на Go для поиска рабочих пар **резолвер + релей** для `dnscrypt-proxy`
с анонимизацией (Anonymized DNSCrypt / ODoH).

Собирает матрицу «резолвер × релей», отсеивает мёртвые/неподходящие варианты
и выдаёт **готовый блок конфига** для `/etc/dnscrypt-proxy/dnscrypt-proxy.toml`.

## Зачем

`dnscrypt-proxy` умеет анонимизировать запросы через релеи, но **не все пары
(резолвер, релей) работают**. Некоторые релеи блокируют «свои» резолверы, некоторые
резолверы форвардят в Google, некоторые релеи лежат только в РФ или Азии.

Вручную это выяснять — часы. `dnsrelay` делает это за минуты и сразу даёт конфиг.

## Что умеет

- **`auto`** — полный конвейер: фильтр живых резолверов → фильтр живых релеев →
  матрица → готовый конфиг. Одна команда.
- **`dnscrypt`** — фильтр живых DNSCrypt-релеев.
- **`resolvers-filter`** — фильтр живых DNSCrypt-резолверов.
- **`matrix`** — матрица «резолвер × релей» + рекомендации.
- **`tcp`** — быстрая проверка доступности релеев (только для отладки).
- **`odoh`** — проверка ODoH-релеев и целей.

### Фичи

- ✅ **Запись готового конфига** в `*-routes.toml`
- ✅ **Прогресс в реальном времени** с ETA
- ✅ **Фильтр «не РФ»** — исключение релеев по паттернам
- ✅ **Автоотсев Google-форвардеров** — через `whoami.akamai.net`
- ✅ **Дедупликация релеев** — один релей не более чем в 3 резолверах
- ✅ **Инкрементальный дамп** — не теряет результаты при `Ctrl+C`
- ✅ **`-only-eu`** — фильтр «только европейские релеи»

## Сборка

Нужен Go 1.21+.

```bash
# Для текущей машины
go build -ldflags="-s -w" -o dnsrelay

# Под ARM64 (Routerich, современные роутеры)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o dnsrelay-arm64

# Под MIPS (старые роутеры, OpenWrt)
CGO_ENABLED=0 GOOS=linux GOARCH=mips GOMIPS=softfloat go build -ldflags="-s -w" -o dnsrelay-mips

# Под ARMv7 (Mikrotik и др.)
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w" -o dnsrelay-armv7
```

## Файлы данных

Скачиваются один раз в папку с утилитой:

```bash
# Резолверы (основной список)
wget https://raw.githubusercontent.com/DNSCrypt/dnscrypt-resolvers/master/v3/public-resolvers.md -O public-resolvers.md

# Релеи (основной список)
wget https://raw.githubusercontent.com/DNSCrypt/dnscrypt-resolvers/master/v3/relays.md -O relays.md

# dnscry.pt (дополнительные релеи и резолверы)
wget https://www.dnscry.pt/resolvers.md -O dnscry.pt-resolvers.md

# Quad9 (отдельный список)
wget https://raw.githubusercontent.com/Quad9DNS/dnscrypt-settings/main/dnscrypt/quad9-resolvers.md -O quad9-resolvers.md

# Объединённый список релеев (рекомендуется)
cat relays.md dnscry.pt-resolvers.md > relays-all.md
```

## Использование

### 1. Полный авто-конвейер (основной сценарий)

```bash
./dnsrelay -mode auto \
  -resolvers public-resolvers.md \
  -f relays.md \
  -c 50 \
  -timeout 3s \
  -top 100 \
  -out-prefix mxlinux \
  2>&1 | tee mxlinux-auto.log
```

**Что произойдёт:**
1. Отфильтрует живые резолверы → `mxlinux-resolvers-alive.md`
2. Отфильтрует живые релеи → `mxlinux-relays-alive.md`
3. Возьмёт топ-100 из каждого (300 строк)
4. Построит матрицу 100×100 = 10 000 пар → `mxlinux-matrix.tsv`
5. Напечатает готовый конфиг + запишет `mxlinux-matrix-routes.toml`

**Время:** 15–25 минут на ПК, 30–60 минут на роутере.

**Одна команда — полный конфиг на выходе.**

### 2. Только живые релеи

```bash
./dnsrelay -mode dnscrypt \
  -f relays-all.md \
  -c 50 -timeout 5s -proto udp \
  -out-file relays-alive.md
```

Время: 1–2 минуты. Результат — `relays-alive.md`.

### 3. Только живые резолверы

```bash
./dnsrelay -mode resolvers-filter \
  -resolvers public-resolvers.md \
  -c 50 -timeout 5s \
  -resolvers-filter-out resolvers-alive.md
```

Время: 2–5 минут. Результат — `resolvers-alive.md`.

### 4. Матрица вручную

```bash
./dnsrelay -mode matrix \
  -resolvers resolvers-alive.md \
  -f relays-alive.md \
  -c 50 -timeout 3s \
  -matrix-out matrix.tsv \
  -top 10
```

Результаты:
- `matrix.tsv` — таблица `резолвер × релей`:
  - число = задержка в мс
  - `X` = TIMEOUT
  - `-` = не проверялось
- `matrix-routes.toml` — готовый конфиг на 10 резолверов

### 5. ODoH

```bash
./dnsrelay -mode odoh -c 10 -timeout 10s
```

Проверяет ODoH-релеи и цели. Результат — `odoh-results.txt` в формате `relay|target|latency`.

## Все флаги

| Флаг | По умолчанию | Описание |
|---|---|---|
| `-mode` | `tcp` | Режим: `auto`, `dnscrypt`, `resolvers-filter`, `matrix`, `tcp`, `odoh` |
| `-f` | `/etc/dnscrypt-proxy/relays.md` | Файл релеев |
| `-resolvers` | `/etc/dnscrypt-proxy2/public-resolvers.md` | Файл резолверов |
| `-c` | `50` | Количество воркеров |
| `-timeout` | `5s` | Таймаут запроса |
| `-proto` | `udp` | Протокол до релея: `udp` или `tcp` |
| `-top` | `5` | Сколько резолверов рекомендовать |
| `-out-prefix` | `auto` | Префикс выходных файлов (для `-mode auto`) |
| `-matrix-out` | `matrix.tsv` | Куда писать матрицу |
| `-out-file` | — | Куда писать живые релеи (для `-mode dnscrypt`) |
| `-resolvers-filter-out` | `resolvers-alive.md` | Куда писать живые резолверы |
| `-exclude-relay-pattern` | `moscow,russia,msk,spb` | Подстроки для исключения релеев (через запятую) |
| `-only-eu` | `false` | Оставить только европейские релеи |
| `-v` | `false` | Подробный вывод |
| `-stamp` | AdGuard DNS | Stamp резолвера для проверки релеев |
| `-odoh-relays` | `odoh-relays.md` | Файл ODoH-релеев |
| `-odoh-servers` | `odoh-servers.md` | Файл ODoH-целей |
| `-odoh-out` | `odoh-results.txt` | Куда писать результаты ODoH |
| `-odoh-target` | `odoh-cloudflare` | Целевой ODoH-сервер для проверки релеев |

## Примеры использования

### Быстрая проверка (5 минут)

```bash
./dnsrelay -mode auto -top 30 -c 30 -timeout 3s -out-prefix quick
```

30×30 = 900 пар. Даст рабочий конфиг на 5 резолверов.

### Полный прогон без РФ-релеев, только EU (30 минут)

```bash
./dnsrelay -mode auto \
  -top 100 -c 50 -timeout 3s \
  -out-prefix full-eu \
  -only-eu \
  -exclude-relay-pattern "moscow,russia,msk,spb,dnscry.pt-anon-moscow"
```

### Обновить только список живых релеев

```bash
./dnsrelay -mode dnscrypt \
  -f relays-all.md \
  -c 50 -timeout 5s -proto udp \
  -out-file relays-alive.md
```

## Формат выходных файлов

### `*-routes.toml`

Готовый блок для `/etc/dnscrypt-proxy/dnscrypt-proxy.toml`:

```toml
# Автоматически сгенерировано dnsrelay
# Дата: 2026-09-21 10:15:20

server_names = [
    'cs-swe',
    'cs-norway',
    'cs-dus',
    ...
]

[anonymized_dns]
routes = [
    { server_name = 'cs-swe', via = ['dnscry.pt-anon-bremen-ipv4', 'anon-cs-de', 'anon-cs-austria'] },  # health 99%, latencies: 265, 432, 443 мс
    ...
]
```

**Health** — процент работающих пар для этого резолвера (OK/total).
**Latencies** — задержки для выбранных релеев (по возрастанию).

### `*-matrix.tsv`

Таблица:
- **Строки** — резолверы
- **Столбцы** — релеи
- **Значения**: задержка мс / `X` (TIMEOUT) / `-` (не проверялось)

### `*-resolvers-alive.md` / `*-relays-alive.md`

Тот же формат, что и исходные `.md`:

```
## имя
sdns://...
```

Можно использовать как входные файлы.

## Запуск на роутере (OpenWrt, ARM64)

### 1. Собрать ARM64

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -ldflags="-s -w" -o dnsrelay-arm64
```

### 2. Скопировать на роутер

```bash
scp dnsrelay-arm64 public-resolvers.md relays.md dnscry.pt-resolvers.md \
  root@192.168.1.1:/tmp/
```

### 3. Запустить на роутере

```bash
ssh root@192.168.1.1
cd /tmp
chmod +x dnsrelay-arm64

# Полный авто-конвейер с ограничением
./dnsrelay-arm64 -mode auto \
  -resolvers public-resolvers.md \
  -f relays.md \
  -c 5 \
  -timeout 10s \
  -top 50 \
  -out-prefix router \
  -only-eu \
  2>&1 | tee router-auto.log
```

**Важно на роутере:**
- `-c 5` — не больше 5–10 воркеров (мало RAM)
- `-timeout 10s` — путь через VPN длиннее
- `-top 50` — 50×50 = 2500 пар (30–60 минут)

### 4. Применить результат

```bash
# Посмотреть готовый конфиг
tail -40 router-auto.log

# Скопировать server_names и routes в /etc/dnscrypt-proxy2/dnscrypt-proxy.toml
vi /etc/dnscrypt-proxy2/dnscrypt-proxy.toml

# Проверить TOML
python3 -c "import tomllib; tomllib.load(open('/etc/dnscrypt-proxy2/dnscrypt-proxy.toml','rb'))" && echo "TOML OK"

# Перезапустить
/etc/init.d/dnscrypt-proxy restart
sleep 10
logread | grep -i "live servers" | tail -1
```

## Как это работает

### Anonymized DNSCrypt

`dnscrypt-proxy` устанавливает зашифрованную сессию с резолвером, а затем
отправляет запрос **через релей**. Релей не может расшифровать запрос, но
видит IP клиента. Резолвер не видит IP клиента, но видит запрос.

Формат анонимизированного пакета:
```
[0xff × 8] [0x00 0x00] [IPv6-маппинг резолвера (16 байт)] [порт (2 байта BE)] [encrypted payload]
```

Итого 28 байт заголовка перед шифрованным пакетом.

### Почему матрица

Не все пары (резолвер, релей) работают:
- Релей может блокировать соединения «свой к своему» (`cs-de` через `anon-cs-de`).
- Резолвер может форвардить в Google (`dct-*`).
- Релей может быть физически далеко (500+ мс задержки).
- Резолвер может требовать `direct_cert_fallback = true` (AdGuard).

`dnsrelay` перебирает все пары и оставляет только рабочие.

### Фильтр Google-форвардеров

Запрос к `whoami.akamai.net` возвращает **IP резолвера, который делал запрос**.
Если этот IP в подсети Google (`8.8.8.8`, `172.253.*`, `74.125.*` и т.д.) —
резолвер форвардит в Google, и его надо исключить.

Так автоматически отсеиваются `dct-*` и подобные, которые мы вручную
вычисляли через `ipleak.net`.

## Troubleshooting

### `too many open files`

Снизить `-c` до 20–30.

### Все пары TIMEOUT

- Увеличить `-timeout` до 10s.
- Проверить связь с `dnscrypt-proxy` (порт 5353).
- Проверить, что провайдер не блокирует UDP/443.

### `FATAL: toml: line X: ...`

Ошибка в конфиге `dnscrypt-proxy.toml`. Скорее всего, незакрытая скобка
в `routes` или лишняя запятая.

### `live servers: 0`

Все резолверы мертвы. Проверить:
- `public-resolvers.md` актуален.
- Провайдер не блокирует DNS-трафик.
- `dnscrypt-proxy` вообще работает: `journalctl -u dnscrypt-proxy -n 50`.

### Поехали столбцы в TSV

Визуальный баг: `XX` вместо `X X`, `--` вместо `- -`. Не критично —
TSV не используется для парсинга, только для чтения глазами.

## Зависимости

- [github.com/ameshkov/dnscrypt/v2](https://github.com/ameshkov/dnscrypt) — DNSCrypt-клиент
- [github.com/miekg/dns](https://github.com/miekg/dns) — DNS-сообщения
- [github.com/cloudflare/odoh-go](https://github.com/cloudflare/odoh-go) — ODoH

## Лицензия

MIT
```

---

Готово. Сохрани в `~/dnsbrute/README.md`. Если решишь выложить на GitHub — README готов, можно добавить `LICENSE` (MIT) и `.gitignore` (исключить бинарники и `.md`-файлы с данными).

Что дальше — финальный прогон `auto` на MX Linux, или ещё что-то доделать в утилите?
