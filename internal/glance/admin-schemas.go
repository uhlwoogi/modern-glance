package glance

// widgetFieldSchema describes one editable field on a widget for the dialog
// form generator. Marshaled to JSON and consumed by edit-mode.js.
type widgetFieldSchema struct {
	Key       string              `json:"key"`
	Label     string              `json:"label"`
	Type      string              `json:"type"` // string | multiline | number | boolean | select | list-strings | list-objects
	Help      string              `json:"help,omitempty"`
	Required  bool                `json:"required,omitempty"`
	Options   []string            `json:"options,omitempty"`
	Items     []widgetFieldSchema `json:"items,omitempty"` // for list-objects
	Validator string              `json:"validator,omitempty"`
	Lookup    string              `json:"lookup,omitempty"`
}

// widgetSchemas maps widget type to the form fields shown in the dialog
// editor. Widgets not listed here fall back to the textarea YAML editor.
// Keep field order — that's the order the dialog renders them.
var widgetSchemas = map[string][]widgetFieldSchema{
	"rss": {
		{Key: "title", Label: "Custom title", Type: "string", Help: "Override the widget header. Leave blank for the default."},
		{Key: "feeds", Label: "Feeds", Type: "list-objects", Required: true, Items: []widgetFieldSchema{
			{Key: "url", Label: "Feed URL", Type: "string", Required: true, Validator: "rss-feed", Help: "Tested when you save."},
			{Key: "title", Label: "Custom title (optional)", Type: "string", Help: "Defaults to the title from the feed itself."},
		}},
		{Key: "limit", Label: "Items to show", Type: "number", Help: "Default: 25"},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
		{Key: "style", Label: "Style", Type: "select", Options: []string{"vertical-list", "horizontal-cards", "horizontal-cards-2", "detailed-list"}},
	},

	"weather": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "location", Label: "Location", Type: "string", Required: true, Help: "City and country, e.g. London, GB. Start typing to search.", Lookup: "weather-location", Validator: "weather-location"},
		{Key: "units", Label: "Units", Type: "select", Options: []string{"metric", "imperial"}},
		{Key: "hour-format", Label: "Hour format", Type: "select", Options: []string{"24h", "12h"}},
		{Key: "hide-location", Label: "Hide location label", Type: "boolean"},
		{Key: "show-area-name", Label: "Show area name", Type: "boolean"},
	},

	"markets": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "markets", Label: "Symbols", Type: "list-objects", Required: true, Items: []widgetFieldSchema{
			{Key: "symbol", Label: "Ticker symbol", Type: "string", Required: true, Help: "Search a company name or type a symbol like AAPL or BTC-USD.", Lookup: "market-symbol", Validator: "market-symbol"},
			{Key: "name", Label: "Display name (optional)", Type: "string"},
		}},
		{Key: "sort-by", Label: "Sort by", Type: "select", Options: []string{"absolute-change", "relative-change"}},
	},

	"monitor": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "sites", Label: "Sites", Type: "list-objects", Required: true, Items: []widgetFieldSchema{
			{Key: "title", Label: "Display name", Type: "string", Required: true},
			{Key: "url", Label: "URL", Type: "string", Required: true},
			{Key: "icon", Label: "Icon URL (optional)", Type: "string"},
			{Key: "alt-status-codes", Label: "Other OK status codes (comma-separated)", Type: "string", Help: "e.g. 401,403 — codes that should still show OK."},
		}},
		{Key: "show-failing-only", Label: "Show failing only", Type: "boolean"},
	},

	"reddit": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "subreddit", Label: "Subreddit", Type: "string", Required: true, Help: "Just the name, no /r/"},
		{Key: "sort-by", Label: "Sort by", Type: "select", Options: []string{"hot", "new", "top", "rising"}},
		{Key: "top-period", Label: "Top period (when sort-by is 'top')", Type: "select", Options: []string{"day", "week", "month", "year", "all"}},
		{Key: "show-thumbnails", Label: "Show thumbnails", Type: "boolean"},
		{Key: "show-flairs", Label: "Show flairs", Type: "boolean"},
		{Key: "limit", Label: "Items to show", Type: "number"},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
		{Key: "style", Label: "Style", Type: "select", Options: []string{"vertical-list", "horizontal-cards"}},
	},

	"hacker-news": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "sort-by", Label: "Sort by", Type: "select", Options: []string{"top", "new", "best"}},
		{Key: "limit", Label: "Items to show", Type: "number"},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
	},

	"lobsters": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "sort-by", Label: "Sort by", Type: "select", Options: []string{"hot", "new"}},
		{Key: "tags", Label: "Tags (filter)", Type: "list-strings"},
		{Key: "limit", Label: "Items to show", Type: "number"},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
	},

	"videos": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "channels", Label: "YouTube channel IDs", Type: "list-strings", Required: true, Help: "The UC... ID, not the @handle"},
		{Key: "limit", Label: "Videos to show", Type: "number"},
		{Key: "style", Label: "Style", Type: "select", Options: []string{"horizontal-cards", "grid-cards", "vertical-list"}},
	},

	"twitch-channels": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "channels", Label: "Twitch channel names", Type: "list-strings", Required: true},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
	},

	"repository": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "repository", Label: "Repository", Type: "string", Required: true, Help: "owner/name, e.g. glanceapp/glance"},
		{Key: "token", Label: "GitHub token (optional)", Type: "string", Help: "Increases rate limits. Use ${env:VAR} to read from env."},
		{Key: "pull-requests-limit", Label: "Pull requests to show", Type: "number"},
		{Key: "issues-limit", Label: "Issues to show", Type: "number"},
		{Key: "commits-limit", Label: "Commits to show", Type: "number"},
	},

	"releases": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "repositories", Label: "Repositories", Type: "list-strings", Required: true, Help: "owner/name per line. Add prefixes like docker:, gitlab:, codeberg: for non-GitHub sources."},
		{Key: "token", Label: "GitHub token (optional)", Type: "string"},
		{Key: "show-source-icon", Label: "Show source icon", Type: "boolean"},
		{Key: "limit", Label: "Releases to show", Type: "number"},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
	},

	"bookmarks": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "groups", Label: "Groups", Type: "list-objects", Required: true, Items: []widgetFieldSchema{
			{Key: "title", Label: "Group title", Type: "string", Required: true},
			{Key: "color", Label: "Color (HSL, optional)", Type: "string", Help: "e.g. 200 50 50"},
			{Key: "links", Label: "Links", Type: "list-objects", Required: true, Items: []widgetFieldSchema{
				{Key: "title", Label: "Link title", Type: "string", Required: true},
				{Key: "url", Label: "URL", Type: "string", Required: true},
				{Key: "icon", Label: "Icon URL (optional)", Type: "string"},
			}},
		}},
	},

	"clock": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "hour-format", Label: "Hour format", Type: "select", Options: []string{"24h", "12h"}},
		{Key: "timezones", Label: "Extra timezones to show", Type: "list-objects", Items: []widgetFieldSchema{
			{Key: "timezone", Label: "Timezone (e.g. Europe/London)", Type: "string", Required: true},
			{Key: "label", Label: "Display label", Type: "string"},
		}},
	},

	"calendar": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "first-day-of-week", Label: "First day of week", Type: "select", Options: []string{"monday", "sunday"}},
		{Key: "show-week-numbers", Label: "Show week numbers", Type: "boolean"},
	},

	"search": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "search-engine", Label: "Search engine", Type: "select", Options: []string{"duckduckgo", "google", "bing", "kagi", "startpage", "perplexity"}},
		{Key: "new-tab", Label: "Open results in new tab", Type: "boolean"},
		{Key: "autofocus", Label: "Autofocus on page load", Type: "boolean"},
	},

	"iframe": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "source", Label: "URL to embed", Type: "string", Required: true},
		{Key: "height", Label: "Height (CSS)", Type: "string", Help: "e.g. 400px, 50vh"},
	},

	"html": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "source", Label: "HTML", Type: "multiline", Required: true},
	},

	"server-stats": {
		{Key: "title", Label: "Custom title", Type: "string"},
	},

	"to-do": {
		{Key: "title", Label: "Custom title", Type: "string"},
	},

	"dns-stats": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "service", Label: "Service", Type: "select", Required: true,
			Options: []string{"adguard", "pihole", "pihole-v6", "technitium"}},
		{Key: "url", Label: "Service URL", Type: "string", Required: true,
			Help: "Base URL of your DNS resolver, e.g. http://pi.hole or http://192.168.1.10"},
		{Key: "username", Label: "Username (AdGuard / Pi-hole v6)", Type: "string"},
		{Key: "password", Label: "Password", Type: "string",
			Help: "Use ${env:DNS_PASSWORD} to read from an env var instead of inlining."},
		{Key: "token", Label: "API token (Pi-hole v5, Technitium)", Type: "string"},
		{Key: "allow-insecure", Label: "Allow insecure TLS (self-signed certs)", Type: "boolean"},
		{Key: "hide-graph", Label: "Hide hourly graph", Type: "boolean"},
		{Key: "hide-top-domains", Label: "Hide top blocked/queried domains", Type: "boolean"},
		{Key: "hour-format", Label: "Hour format", Type: "select",
			Options: []string{"24h", "12h"}},
	},

	"docker-containers": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "sock-path", Label: "Docker socket path", Type: "string",
			Help: "Default: /var/run/docker.sock"},
		{Key: "category", Label: "Category filter", Type: "string",
			Help: "Show only containers labelled glance.category=<value>"},
		{Key: "hide-by-default", Label: "Hide containers unless labelled glance.hide=false", Type: "boolean"},
		{Key: "running-only", Label: "Show running containers only", Type: "boolean"},
		{Key: "format-container-names", Label: "Format container names", Type: "boolean",
			Help: "Title-cases names like 'home_assistant' → 'Home Assistant'"},
	},

	"extension": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "url", Label: "Extension URL", Type: "string", Required: true,
			Help: "URL of the Glance extension endpoint. See the Extensions docs."},
		{Key: "fallback-content-type", Label: "Fallback content type", Type: "select",
			Options: []string{"", "html", "iframe"},
			Help: "Used when the response has no Widget-Content-Type header."},
		{Key: "allow-potentially-dangerous-html", Label: "Allow raw HTML output", Type: "boolean",
			Help: "Only enable for extensions you trust — they can inject scripts otherwise."},
	},

	"change-detection": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "instance-url", Label: "ChangeDetection.io URL", Type: "string",
			Help: "Defaults to https://www.changedetection.io. Use your own instance for self-hosted."},
		{Key: "token", Label: "API token", Type: "string",
			Help: "From your changedetection.io profile. Use ${env:CD_TOKEN} for env-based config."},
		{Key: "watches", Label: "Watch UUIDs (optional)", Type: "list-strings",
			Help: "Filter to specific watches. Leave empty to show all."},
		{Key: "limit", Label: "Items to show", Type: "number"},
		{Key: "collapse-after", Label: "Collapse after N items", Type: "number"},
	},

	"custom-api": {
		{Key: "title", Label: "Custom title", Type: "string"},
		{Key: "url", Label: "API URL", Type: "string", Required: true},
		{Key: "method", Label: "HTTP method", Type: "select",
			Options: []string{"GET", "POST", "PUT", "PATCH", "DELETE"}},
		{Key: "body-type", Label: "Body type (when POST/PUT/PATCH)", Type: "select",
			Options: []string{"", "json", "string"}},
		{Key: "body", Label: "Request body", Type: "multiline",
			Help: "Optional. Sent as the request body."},
		{Key: "template", Label: "Output template (Go html/template)", Type: "multiline", Required: true,
			Help: "Renders the response. See the Custom API docs for available helpers like {{ .JSON.String \"path.to.field\" }}."},
		{Key: "frameless", Label: "Hide widget frame", Type: "boolean"},
	},

	// group + split-column intentionally skipped: their `widgets:` field is a
	// recursive list of widget objects, which the inline form generator can't
	// render usefully yet. They fall through to the YAML editor.
}
