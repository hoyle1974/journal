# Telegram interface – final setup

The Jot app exposes a Telegram Bot webhook similar to the Twilio SMS flow: incoming messages are accepted, processed by the FOH (query) agent, and the reply is sent back via the Telegram Bot API.

## What’s already in place

- **`pkg/telegram`**: Webhook parsing, optional secret-token validation, `SendMessage`.
- **Config**: `TELEGRAM_BOT_TOKEN`, `TELEGRAM_SECRET_TOKEN`, `ALLOWED_TELEGRAM_USER_ID` (optional).
- **API**: `POST /telegram` (webhook), `POST /internal/process-telegram-query` (Cloud Task).
- **Service**: `TelegramService` + `ProcessIncomingTelegram` (runs FOH with source `"telegram"`).

## Steps to wire it to Telegram

### 1. Create a bot and get the token

1. In Telegram, open [@BotFather](https://t.me/BotFather).
2. Send `/newbot` and follow the prompts (name, username).
3. Copy the **bot token** (e.g. `123456789:ABCdefGHI...`).

### 2. Store secrets

- **`.env` (required for webhook + local)**: Add the token to your `.env` file (copy from `.env.example` if needed). This is used by `deploy.sh` to set the Telegram webhook after deploy and by the app when running locally. Never commit `.env`.

  ```bash
  TELEGRAM_BOT_TOKEN=123456789:ABCdefGHI...
  # Optional: restrict to your user ID (see step 4)
  ALLOWED_TELEGRAM_USER_ID=987654321
  # Optional: secret for webhook verification (set in step 5 when calling setWebhook)
  TELEGRAM_SECRET_TOKEN=your-random-secret
  ```

- **GCP**: Run `./scripts/setup-secrets.sh <dev|prod>` and enter `TELEGRAM_BOT_TOKEN` when prompted (and optionally the others). The script creates/updates Secret Manager and grants the Cloud Run service account access.

### 3. Deploy

Deploy so the webhook URL is reachable over HTTPS:

```bash
./scripts/deploy.sh <dev|prod>
```

Your webhook URL will be:

- **Cloud Functions**: `https://us-central1-<PROJECT>.cloudfunctions.net/jot-api-go/telegram`
- **Cloud Run** (if you use that): `https://<your-service-url>/telegram`

Use the actual base URL of your deployed API (e.g. from the deploy output or `JOT_API_URL`).

### 4. (Optional) Restrict to your user ID

To only respond to a specific Telegram user:

1. Send a message to your bot (any text).
2. Visit in a browser (replace `<BOT_TOKEN>` with your token):
   `https://api.telegram.org/bot<BOT_TOKEN>/getUpdates`
3. In the JSON, find `"from":{"id":123456789,...}` — that number is your user ID.
4. Set `ALLOWED_TELEGRAM_USER_ID=123456789` in Secret Manager (or `.env` for local) and redeploy if needed.

### 5. Set the webhook in Telegram

**Option A – Automatic (recommended):** If `TELEGRAM_BOT_TOKEN` (and optionally `TELEGRAM_SECRET_TOKEN`) are set in your env file (e.g. `.env` or `.env.prod`) when you run deploy, `./scripts/deploy.sh` will call Telegram’s `setWebhook` for you after a successful deploy. No need to run the curl commands below.

**Option B – Manual:** Tell Telegram to send updates to your Jot webhook URL.

**Without secret token (simplest):**

```bash
curl -X POST "https://api.telegram.org/bot<BOT_TOKEN>/setWebhook" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://us-central1-<PROJECT>.cloudfunctions.net/jot-api-go/telegram"}'
```

**With secret token (recommended in production):**

1. Generate a random string (e.g. `openssl rand -hex 24`) and set it as `TELEGRAM_SECRET_TOKEN` in Secret Manager (and in the setWebhook call below).
2. Redeploy so the app has the new secret.
3. Set the webhook with the same secret:

```bash
curl -X POST "https://api.telegram.org/bot<BOT_TOKEN>/setWebhook" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://us-central1-<PROJECT>.cloudfunctions.net/jot-api-go/telegram","secret_token":"YOUR_SECRET_TOKEN"}'
```

Replace:

- `<BOT_TOKEN>` with your bot token.
- The `url` with your real Jot API base + `/telegram`.
- `YOUR_SECRET_TOKEN` with the value you stored in `TELEGRAM_SECRET_TOKEN`.

### 6. Verify

1. Send a message to your bot in Telegram (e.g. “What did I log yesterday?”).
2. You should get a reply from the FOH agent. Entries and queries will use source `"telegram"`.

To clear the webhook (e.g. to stop Telegram from sending updates):

```bash
curl -X POST "https://api.telegram.org/bot<BOT_TOKEN>/deleteWebhook"
```

## Behaviour summary

- **Webhook**: `POST /telegram` — validates optional secret token, parses the Update, checks optional user allowlist, returns 200 quickly and enqueues a Cloud Task (or runs in a goroutine if tasks are unavailable).
- **Task**: `POST /internal/process-telegram-query` — runs the same FOH query flow as SMS, then sends the reply with `SendMessage(chat_id, response)`.

Same as SMS: only text (and caption) is used; non-message updates (e.g. channel posts) are ignored with 200 OK.
