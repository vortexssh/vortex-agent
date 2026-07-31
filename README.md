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

1. Один раз соберите артефакты: `make cross` и выложите их на CDN (`VITE_AGENT_BINARY_BASE_URL` во Web).
2. В панели: **Hosts → Install agent** — получите one-liner / `install.sh` с уже вшитыми `agent_id` + `secret`.
3. На сервере: вставьте one-liner (`echo '…' | base64 -d | sudo bash`).

Бинарник общий; на каждый хост меняется только `/etc/vortex-agent.env`.

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
