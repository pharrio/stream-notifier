# Broadcast Box Stream Notifier

A lightweight Discord notifier for [Broadcast Box](https://github.com/Glimesh/broadcast-box).
Add it alongside an existing Broadcast Box installation; it does not authenticate
streams, manage profiles, or change your streaming server.

One static Go binary. No database, persistent volume, webhook listener, inbound
port, Python runtime, privileged Discord intents, or slash commands.

## What You Get

- One dashboard per notification channel, listing live stream names as watch links.
- One card per live session, updated with duration, viewers, and video upload bitrate.
- Automatic removal of ended or stalled streams, even when viewers keep a page open.
- Recovery of existing cards after restarts, without purging the channel.
- English by default, with a built-in Simplified Chinese interface.
- A native Discord timestamp on the dashboard as a service heartbeat.

MOTD is intentionally hidden. The dashboard title is not clickable; stream names
and live-card titles link directly to the public stream page. The blue accent
matches the official project mark, shown through a fixed thumbnail URL, not an
uploaded attachment.

## Before You Start

You need:

- A working Broadcast Box instance with `/api/status` enabled and reachable from the notifier.
- Docker Engine and Docker Compose v2.
- A Discord account allowed to install bots in the target server, and permission to
  configure the notification channel.

The image supports Linux `amd64` and `arm64`. Broadcast Box can run in Docker or
as a host binary. It does not need to be in the same Compose project.

## 1. Create Your Discord Bot

1. Sign in to the [Discord Developer Portal](https://discord.com/developers/applications).
2. Choose **New Application**, enter a name such as `Broadcast Box Notifier`, review
   Discord's developer terms and policy, and create the application.
3. Open **Bot** in the application sidebar. New applications already have a bot user;
   if your account shows an **Add Bot** button instead, create the bot there.
4. Under **Token**, choose **Reset Token**, complete any password or 2FA prompt, and
   copy the new token. Discord only shows it when generated.
5. Leave **Requires OAuth2 Code Grant** disabled. Leave all three **Privileged
   Gateway Intents** disabled: Presence, Server Members, and Message Content.

Use the **bot token**, not the Application ID, Public Key, or OAuth2 Client Secret.
Do not prefix it with `Bot `. Keep it private; anyone holding it can act as your bot.
Do not post it in an issue, chat, screenshot, or committed Compose file.

No interactions endpoint, redirect URI, webhook URL, or command registration is needed.
The [official Discord setup guide](https://docs.discord.com/developers/quick-start/getting-started)
provides additional background; its example bot needs features this notifier does not.

## 2. Invite It With Minimum Permissions

In the application's **OAuth2 → OAuth2 URL Generator**:

1. Select the **bot** scope.
2. Select **Guild Install** if an installation-type selector is shown.
3. Select only these four permissions:

| Permission | Why it is needed |
| --- | --- |
| View Channels / View Channel | Access the notification channel |
| Send Messages | Create dashboard and live cards |
| Embed Links | Display the cards |
| Read Message History | Recover and deduplicate the bot's cards after restarts |

The combined permissions integer is **84992**. Administrator, Manage Messages,
Attach Files, Mention Everyone, voice permissions, and privileged intents are not needed.
The notifier does not create slash commands; do not add command-related scopes just for it.

Open the generated URL, select your server, and authorize the installation. If
Discord launches its desktop app, complete the installation there or choose the
web continuation. Complete any verification prompt yourself.

A direct invitation URL has this form; replace the Application ID:

```text
https://discord.com/oauth2/authorize?client_id=YOUR_APPLICATION_ID&permissions=84992&integration_type=0&scope=bot
```

### Configure the Channel

Create a normal server **text channel**, or choose an existing one. Ensure the bot
has all four permissions above **in that channel**, not just on its server role.
Private-channel or category overrides can deny permissions granted during installation.

For a read-only notification channel, deny **Send Messages** to ordinary members
and explicitly allow the bot's role. The bot does not delete members' messages.
Server owners and users with Administrator permission can still speak because
Administrator bypasses channel overrides.

If the channel is private, explicitly allow the bot to view it as well. Recheck
category synchronization and role/member overrides if access is denied. See
[Discord's permission rules](https://docs.discord.com/developers/topics/permissions).

### Copy the Channel ID

In Discord, enable **User Settings → Advanced → Developer Mode**. Right-click the
notification channel and choose **Copy Channel ID**. Use the channel ID, not the
server ID, bot ID, or a Discord channel URL.

## 3. Choose a Deployment Method

Keep Broadcast Box's existing configuration. Add only the notifier service that
matches your networking setup. No notifier ports or volumes are required.

### A. Any Reachable Public Instance

If `/api/status` is reachable without an interactive login, use [compose.yaml](compose.yaml).
Create a private deployment directory, place the file there, and edit its three
active environment values:

```yaml
services:
  stream-notifier:
    image: ghcr.io/pharrio/stream-notifier:latest
    restart: unless-stopped
    environment:
      DISCORD_TOKEN: "YOUR_BOT_TOKEN"
      CHANNEL_IDS: "YOUR_NOTIFICATION_CHANNEL_ID"
      BROADCAST_BOX_PUBLIC_URL: "https://stream.example.com"
    read_only: true
    cap_drop: [ALL]
    security_opt:
      - no-new-privileges:true
```

Then:

```sh
chmod 600 compose.yaml
docker compose config --quiet
docker compose up -d stream-notifier
docker compose logs --tail 50 stream-notifier
```

No `.env` file is required. Configuration is ordinary environment variables, set
explicitly in Compose. The program does not load dotenv files.

### B. Official Broadcast Box Compose: Linux Host Network

The [official Broadcast Box Compose file](https://github.com/Glimesh/broadcast-box/blob/main/docker-compose.yaml)
uses host networking. Copy [examples/compose.host.yaml](examples/compose.host.yaml)
into the same directory as `compose.notifier.yaml` and fill in the token, channel,
and public URL.

This adds a host-network notifier querying `http://127.0.0.1:8080` internally.
Change that address if Broadcast Box uses a different HTTP port.

```sh
docker compose -f docker-compose.yaml -f compose.notifier.yaml config --quiet
docker compose -f docker-compose.yaml -f compose.notifier.yaml up -d --no-deps stream-notifier
```

These commands add the notifier without restarting Broadcast Box, Caddy, or
Watchtower. Use the same `-f` arguments for subsequent notifier commands.

Host networking in this example assumes Linux. Docker Desktop networking on
macOS/Windows is different; use a reachable public URL or an appropriate host address.

### C. Broadcast Box on a Docker Bridge Network

Add the service from [examples/compose.bridge.yaml](examples/compose.bridge.yaml)
to your existing stack. It queries `http://broadcast-box:8080` using Docker DNS.
Both services must share a network, and `broadcast-box` must match the service name
or a network alias. If your stack disables the default network, attach the notifier
to the existing shared network explicitly.

```sh
docker compose config --quiet
docker compose up -d --no-deps stream-notifier
```

For a separate Compose project, connect it to Broadcast Box's existing network:

```yaml
networks:
  default:
    external: true
    name: YOUR_EXISTING_BROADCAST_BOX_NETWORK
```

Find the actual network name with `docker network ls` or inspect the Broadcast Box
container. Docker service DNS does not work across unrelated networks.
`127.0.0.1` inside a bridge container refers to that container, not the Docker host.

### D. Broadcast Box Running as a Host Binary

On Linux, run the notifier container with the host-network example from method B,
using the standalone example file rather than Broadcast Box's Compose file:

```sh
docker compose -f compose.notifier.yaml up -d stream-notifier
```

### Public URL Versus API URL

`BROADCAST_BOX_PUBLIC_URL` is the address Discord readers open. It must not be an
internal Docker service name. `BROADCAST_BOX_API_URL` is an optional separate base
address for polling; when omitted, it uses the public URL.

Give both settings the **base URL**, not the full `/api/status` path.
Internal polling avoids routing through public DNS, TLS proxies, and interactive
login pages. It also allows the frontend to remain password protected.

If using Pangolin or another authentication proxy, prefer internal API polling.
Do not bypass authentication for `/api/*` indiscriminately; that may expose admin
or other endpoints. The notifier needs status reads only and does not need an
admin token or any access to Broadcast Box profiles.

## 4. Verify the Installation

1. Check the container logs for `Notifier started`, the selected language, and interval.
2. Check the channel for one dashboard from your new bot.
3. Wait two polling cycles: the dashboard's native timestamp should advance.
4. Start a test stream. A watch link should appear on the dashboard and one live card
   should show duration, viewers, and video upload bitrate.
5. Stop the stream. Its card should disappear. If Broadcast Box retains it while
   viewers stay connected, allow two unchanged polling comparisons for ghost detection.
6. Restart the notifier. It should reuse the existing dashboard, not clear the channel
   or send another notification for an already-stalled stream.

The bot can appear **offline in Discord's member list** because it uses REST only,
not a Gateway connection. This is expected. The dashboard timestamp is the heartbeat.

At process startup, streams already listed by the API are checked for packet growth
before any new live notification is sent. Existing cards are preserved during this
confirmation. A dashboard may briefly show **Checking stream activity**.

## Configuration

Only the first three settings are required. There are no per-text templates or
separate ghost-timeout settings to maintain.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DISCORD_TOKEN` | Required | Bot token without the `Bot ` prefix |
| `CHANNEL_IDS` | Required | One channel ID, or comma-separated channel IDs |
| `BROADCAST_BOX_PUBLIC_URL` | Required | Public watch-page base URL |
| `BROADCAST_BOX_API_URL` | Public URL | Optional internal status API base URL |
| `LANGUAGE` | `en` | `en` or `zh-CN` |
| `CHECK_INTERVAL_SECONDS` | `10` | Delay between polling cycles; integer from 5 to 3600 |
| `LOG_CHANNEL_ID` | Disabled | Separate channel for API failure/recovery notices |
| `DASHBOARD_THUMBNAIL_URL` | Official logo URL | Optional HTTP(S) image URL; queries are allowed |

For Chinese, add `LANGUAGE: "zh-CN"` to `environment`. Texts are bundled in
`locales/*.json`; changing those files requires rebuilding. API/public base URLs
cannot contain credentials, queries, or fragments. Image URLs may contain queries,
but not credentials or fragments.

A log channel must be different from the notification channels. Grant the bot
View Channel and Send Messages there. Console logging works without a log channel.
Outage notices are sent on the first failure, at most once per 15 minutes while
unavailable, and once on recovery. Sensitive token values and request bodies are
not logged; Discord errors retain status, error code, and safe field paths.

### Stream State and Metrics

- A complete empty response, including JSON `null`, means no streams; absent streams
  are removed immediately.
- A valid JSON body is accepted even if Broadcast Box serves it as `text/plain`.
- Received audio/video packet counters establish activity. Two consecutive unchanged
  comparisons hide a ghost; packet growth restores it. Counter resets establish a
  new baseline rather than proving activity by themselves.
- At the default interval, ghost tolerance is approximately 20 seconds plus request
  processing time. Adjusting the polling interval changes this window too.
- Failed polls do not advance ghost counters. Invalid entries make a response partial;
  valid entries can update, but omitted streams and existing cards are preserved.
- Duration is `45s`, `39m`, or `1h 39m`; seconds are omitted after the first minute.
- Viewers is the number of sessions reported by Broadcast Box, not unique people.
- Bitrate is the **highest current video-layer upload bitrate**, converted from
  bytes/second to Mbps with one decimal place. Layers are not summed, and it is not
  the receive bitrate of an individual viewer. Audio is not included.

A changed `streamStart` identifies a new session. Cards are recovered using the bot's
own message structure, stream name, and start timestamp, not translated titles or
the current public hostname. The first history scan is complete; subsequent
five-minute checks scan only messages newer than the saved in-memory cursor.
No state is written to disk. Do not run multiple instances with the same bot and
notification channels: reconciliation is not a distributed lock.

## Updating and Stopping

```sh
docker compose pull stream-notifier
docker compose up -d --no-deps stream-notifier
```

Use the file arguments from your chosen deployment method. This updates only the
notifier. `latest` follows published builds; choose a commit tag from GHCR if you
want to pin a specific build. To roll back, set the previous image tag and recreate
the notifier.

After changing environment values, run `docker compose up -d --no-deps stream-notifier`.
A plain `docker compose restart` does **not** load changed environment values.

To stop it, use `docker compose stop stream-notifier`. To remove this standalone
notifier project, use `docker compose down`. Do not run `down` on a combined
Broadcast Box stack unless you intend to stop the other services as well.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Discord login returns HTTP 401 | Use the Bot token, not the Client Secret/Public Key. If reset, update the token and recreate the container. |
| HTTP 403, code 50001 or 50013 | Verify membership, View Channel, channel/category overrides, and the four required channel permissions. |
| HTTP 404, code 10003 | Verify the channel ID and that this bot can access the channel. |
| Dashboard says status is unavailable | Check API base URL, DNS, HTTP port, authentication redirects, certificates, and `/api/status`. |
| `DNS resolution failed` for `broadcast-box` | Attach both containers to the same bridge network or use the host/public address appropriate to the deployment. |
| Bot looks offline but cards update | Expected with REST-only operation. |
| Card text has no thumbnail | Check client embed/image display settings and whether Discord can fetch the configured image URL. |
| Old cards remain or duplicates appear | Grant Read Message History; stop extra instances; use the same bot token when restarting. Other bots' messages are deliberately untouched. |
| Members can post in the notification channel | Configure channel permissions; this bot is not a moderation bot. Administrators bypass denials. |

Use `docker compose logs --tail 100 stream-notifier` for diagnostics. Validate Compose
with `docker compose config --quiet`; the non-quiet form expands and displays secrets.
The runtime image is `scratch`: it contains no shell, `curl`, or package manager.
Do not rely on `docker exec ... sh` for debugging.

The public API can be checked from a suitable host with:

```sh
curl -fsS https://stream.example.com/api/status
```

For Docker-only internal DNS, run a temporary diagnostic client on the same network:

```sh
docker run --rm --network YOUR_BROADCAST_BOX_NETWORK \
  curlimages/curl:latest -fsS http://broadcast-box:8080/api/status
```

Never post real tokens in diagnostic output. If a token is exposed, reset it in the
Developer Portal and recreate the notifier with the replacement token.

## License and Attribution

[MIT](LICENSE). Commercial use, modification, and redistribution are permitted under
its terms; retain the copyright and license notice. Third-party dependencies retain
their own licenses, reproduced in `third_party/` and `/licenses` in the container.

The thumbnail uses the [official Glimesh mark](https://github.com/Glimesh/glimesh.tv/blob/1fb679f0dcc04154034a6b73e7b52ac2677493f1/priv/static/images/logos/logo-no-text.png)
through a commit-pinned GitHub URL. It is not bundled or uploaded to Discord.
This project is not affiliated with or endorsed by Discord, Glimesh, or Broadcast Box.
