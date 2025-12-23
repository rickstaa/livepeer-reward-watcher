# Livepeer Reward Watcher

This Go script monitors the Livepeer protocol on Arbitrum and notifies you if your orchestrator's reward call hasn't been made in a round after a configurable delay (the main alert). By default, it also notifies you about successful reward calls and new rounds, but these can be disabled. It serves as an extra safety net alongside the [web3-livepeer-bot](https://github.com/0xVires/web3-livepeer-bot) by @0xVires.

## Features

- Monitors blockchain rounds and reward calls in real-time using Ethereum event subscriptions.
- **Always sends alerts for:**
  - Missing reward calls (core purpose)
  - Connection issues and recovery
  - Subscription errors
- **Also sends alerts for (enabled by default, can be disabled):**
  - Successful reward calls (`--disable-success-alerts`)
  - New round notifications (`--disable-round-alerts`)
- Supports both Telegram and Discord notifications
- Automatic RPC failover with configurable retry limits
- Both the delay and repeat interval for alerts are fully configurable via command-line flags.

## Requirements

- [Go 1.21+](https://go.dev/)
- A working Ethereum WebSocket RPC endpoint (e.g., `wss://arb1.arbitrum.io/ws`).
- Telegram bot token and chat ID (required for Telegram alerts).
- Discord webhook URL (required for Discord alerts).

## Alert Setup Instructions

### Telegram Bot Setup

1. Open Telegram and search for [@BotFather](https://t.me/BotFather).
2. Start a chat and send `/newbot` to create a new bot. Follow the instructions to get your bot token.
3. Start a chat with your new bot (search for its username and click "Start").
4. To get your chat ID:

   - Send a message to your bot.
   - Visit: `https://api.telegram.org/bot<your_bot_token>/getUpdates` in your browser (replace `<your_bot_token>`).
   - Look for `"chat":{"id":...}` in the response; that's your chat ID.
   - For group chats, add the bot to the group, send a message, and use the same method.

5. Set `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` as environment variables.

More info: [Telegram Bot API docs](https://core.telegram.org/bots#botfather)

### Discord Webhook Setup

1. Go to your Discord server and open the channel you want alerts in.
2. Click the gear icon (Edit Channel) > Integrations > Webhooks > New Webhook.
3. Name your webhook and copy the webhook URL.
4. Set `DISCORD_WEBHOOK_URL` as an environment variable.

More info: [Discord Webhooks Guide](https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks)

## Usage

### Building

You need to download ABIs before building the application:

```bash
# Download ABIs first, then build
make download-abis
make build

# Or do both steps manually
make download-abis
go build -o reward_watcher main.go
```

To update ABIs later, just run `make update-abis`.

### Local Setup

Run the script directly on your machine:

```bash
export TELEGRAM_BOT_TOKEN=your_bot_token
export TELEGRAM_CHAT_ID=your_chat_id
export DISCORD_WEBHOOK_URL=your_webhook_url

go run main.go --delay=2h --check-interval=1h <orchestrator-address> [rpc1 rpc2 ...]
```

### Command Line Flags

- `--delay` - Time to wait after new round before warning (default: 2h). Example: `2h`, `30m`
- `--check-interval` - How often to check and repeat warning if reward not called (default: 1h)
- `--repeat` - Repeat warning every check-interval (default: true). Set to false to only warn once per round
- `--disable-success-alerts` - Disable alerts when rewards are successfully called (default: false)
- `--disable-round-alerts` - Disable alerts when new rounds start (default: false)
- `--enable-rpc-alerts` - Enable alerts for RPC disconnects/reconnects and subscription errors (default: false)
- `--max-retry-time` - Max time to retry RPC connections before giving up (default: 30m, 0 = retry forever)

### Usage Examples

```bash
# Minimal setup - only essential alerts (missing rewards + connection issues)
go run main.go 0x123... wss://arb1.arbitrum.io/ws

# Disable successful reward call alerts
go run main.go --disable-success-alerts 0x123... wss://arb1.arbitrum.io/ws

# Disable both successful reward call and new round alerts
go run main.go --disable-success-alerts --disable-round-alerts 0x123... wss://arb1.arbitrum.io/ws

# Custom timing with only new round notifications
go run main.go --delay=1h --check-interval=30m --no-rounds 0x123... wss://arb1.arbitrum.io/ws

# Multiple RPC endpoints for failover
go run main.go 0x123... wss://arb1.arbitrum.io/ws https://arb1.arbitrum.io/rpc
```

### Docker & Docker Compose

Docker and Docker Compose setups are provided for convenience. See:

- [`Dockerfile`](./Dockerfile)
- [`docker-compose.yml`](./docker-compose.yml)

Environment variables for Docker Compose should be set in a `.env` file in this directory. See main variable names in `docker-compose.yml`.

## How it works

- Monitors [`NewRound`](https://arbiscan.io/address/0xdd6f56DcC28D3F5f27084381fE8Df634985cc39f#code) and [`Reward`](https://arbiscan.io/address/0x35Bcf3c30594191d53231E4FF333E8A770453e40#code) events from Livepeer contracts on Arbitrum
- Always alerts for: missing rewards, connection issues, errors
- Also sends alerts for successful rewards and new rounds by default (can be disabled with `--no-success` and `--no-rounds`)
- Automatic RPC failover and reconnection
