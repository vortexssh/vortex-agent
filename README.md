# Vortex Agent

Легковесный хост-демон экосистемы **VortexSSH**. Единый бинарник, только исходящий WSS к Vortex Core (порты не слушает).

## Возможности

- Постоянный WebSocket-туннель с exponential backoff
- Телеметрия (CPU / RAM / Net I/O / Uptime) → Core → Redis
- Web SSH через локальный PTY
- TCP→SSH proxy на `127.0.0.1:22` для GUI/TUI
- Исполнение `task_run` (cron диспатчится на Core)

## Быстрый старт

### Через Vortex Web (рекомендуется)

Web только генерирует one-liner с секретами. Бинарник качается с CDN / GitHub Release
(`VITE_AGENT_BINARY_BASE_URL`), не из образа Web.

1. Опубликуй linux-бинарники куда угодно по HTTPS:
   ```bash
   make cross-linux
   gh release create v0.1.0 bin/vortex-agent-linux-amd64 bin/vortex-agent-linux-arm64
   ```
2. В Web `.env`: `VITE_AGENT_BINARY_BASE_URL=https://github.com/.../releases/download/v0.1.0`
3. **Hosts → Install agent** → one-liner на сервере.

Локально: `make publish-web` + Vite (dev fallback на `/agent`).

### Вручную

```bash
cp .env.example /etc/vortex-agent.env
# VORTEX_CORE_URL, VORTEX_AGENT_ID, VORTEX_SECRET_TOKEN

make build
sudo make install
sudo systemctl enable --now vortex-agent
```

Локальный запуск без systemd:

```bash
export VORTEX_CORE_URL=ws://127.0.0.1:8000
export VORTEX_AGENT_ID=...
export VORTEX_SECRET_TOKEN=...
go run ./cmd/vortex-agent
```

## Конфигурация

| Переменная | Описание | Default |
|---|---|---|
| `VORTEX_CORE_URL` | `ws(s)://host` или полный `/ws/agent` | required |
| `VORTEX_AGENT_ID` | UUID агента из Core | required |
| `VORTEX_SECRET_TOKEN` | plaintext secret (один раз при создании) | required |
| `VORTEX_TELEMETRY_INTERVAL` | секунды между метриками | `5` |
| `VORTEX_HEARTBEAT_INTERVAL_SEC` | heartbeat presence | `30` |
| `VORTEX_TASK_TIMEOUT_SEC` | таймаут скриптов | `300` |
| `VORTEX_SSH_ADDR` | только loopback | `127.0.0.1:22` |

## Протокол

См. Vortex Core README. Auth: `WS /ws/agent?agent_id=&secret=&version=`.

## Разработка

```bash
make test
make vet
make cross   # linux/darwin/windows
```
