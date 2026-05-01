<p align="center"><img src="docs/logo.png"></p>
<h1 align="center">modern-glance</h1>
<p align="center">
  <a href="#installation">Install</a> •
  <a href="#web-based-editor">Editor</a> •
  <a href="docs/configuration.md#configuring-glance">Configuration</a> •
  <a href="docs/themes.md">Themes</a>
</p>

<p align="center">A fork of <a href="https://github.com/glanceapp/glance">glanceapp/glance</a> that adds a web-based dashboard editor so you can manage your config without touching YAML.</p>

> **Upstream credit:** All core functionality — widgets, theming, layout engine, hot-reload — is the work of the [Glance](https://github.com/glanceapp/glance) project and its contributors. This fork adds an editing UI on top and nothing else. If you find Glance useful, consider [sponsoring the upstream project](https://github.com/sponsors/glanceapp).

---

## What's added

### In-place edit mode
A pencil button in the dashboard header toggles edit mode. While active you can:
- **Drag and drop** widgets between columns and reorder them
- **Add widgets** via a form dialog (for most widget types) or a YAML editor fallback
- **Edit widgets** inline with per-field forms — includes live lookup for weather locations, market symbols, and RSS feed validation
- **Delete widgets**
- **Add, delete, resize, and reorder columns**
- **Add and manage head widgets** (the row above the columns)

### Page management (`/edit`)
- Add, delete, and reorder pages
- Per-page settings: name, slug, width, navigation options, mobile header

### Site settings (`/edit/site-settings`)
- App name, logo, favicon, footer — all editable without touching YAML

### Theme settings (`/edit/theme-settings`)
- Color pickers for background, primary, positive, and negative colors (hex input, stored as HSL)
- Contrast and text saturation multipliers
- Light mode toggle, hide-picker toggle, custom CSS file path
- **Preset management**: add, edit, and delete named theme presets with live preview swatches
- **Built-in theme catalog**: 14 themes from the upstream docs (Dracula, Catppuccin variants, Gruvbox, and more) importable with one click

### Multi-step undo
`/edit` keeps a 10-step numbered backup chain (`glance.yml.bak.1` … `.bak.10`). Each save rotates the chain; any backup can be restored individually.

### Auth gate
`/edit` returns 403 unless `auth.users` is configured **or** `admin.allow-without-auth: true` is set. See `docs/glance.yml` for examples of both.

---

## Installation

### Docker (recommended)

```yaml
# docker-compose.yml
services:
  glance:
    image: ghcr.io/uhlwoogi/modern-glance:latest
    ports:
      - "8080:8080"
    volumes:
      # Config dir — glance.yml lives here and is written by /edit
      - ./config:/app/config
      # Optional: custom CSS / images (set server.assets-path: /app/assets in glance.yml)
      - ./assets:/app/assets
    restart: unless-stopped
    # environment:
    #   MY_SECRET_TOKEN: abc123   # reference as ${env:MY_SECRET_TOKEN} in glance.yml
```

1. Create the config directory and grab the starter config:
   ```bash
   mkdir -p config assets
   curl -o config/glance.yml https://raw.githubusercontent.com/uhlwoogi/modern-glance/main/docs/glance.yml
   ```

2. Start:
   ```bash
   docker compose up -d
   ```

3. Open `http://localhost:8080` — click the **pencil icon** in the header to start editing.

> The starter `glance.yml` has `admin.allow-without-auth: true` so `/edit` is open by default. Switch to the auth block in the file before exposing to a network you don't fully trust.

### Updating

```bash
docker compose pull && docker compose up -d
```

---

## Original Glance features

Everything from upstream is intact:

- **Widgets**: RSS, Reddit, Hacker News, weather, YouTube, Twitch, markets, Docker containers, server stats, bookmarks, calendar, monitors, and [many more](docs/configuration.md#configuring-glance)
- **Fast and lightweight**: low memory, minimal JS, single ~20 MB binary, pages load in ~1s
- **Highly customizable**: multiple layouts, pages, themes, custom CSS
- **Mobile optimized**
- **Themeable**: tweak a few numbers or pick from the [theme gallery](docs/themes.md)

Full configuration reference: [docs/configuration.md](docs/configuration.md#configuring-glance)

---

## Building from source

```bash
# Run locally
go run .

# Build binary
go build -o glance .

# Build and push Docker image
docker build -t ghcr.io/uhlwoogi/modern-glance:latest .
docker push ghcr.io/uhlwoogi/modern-glance:latest
```

Requires Go ≥ 1.23.
