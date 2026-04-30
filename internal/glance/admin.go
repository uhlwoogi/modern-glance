package glance

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

var (
	adminIndexTemplate        = mustParseTemplate("admin.html", "document.html", "footer.html")
	adminPageTemplate         = mustParseTemplate("admin-page.html", "document.html", "footer.html")
	adminWidgetTemplate       = mustParseTemplate("admin-widget.html", "document.html", "footer.html")
	adminPageSettingsTemplate = mustParseTemplate("admin-page-settings.html", "document.html", "footer.html")
	adminSiteSettingsTemplate  = mustParseTemplate("admin-site-settings.html", "document.html", "footer.html")
	adminThemeSettingsTemplate = mustParseTemplate("admin-theme-settings.html", "document.html", "footer.html")
)

// allWidgetTypes mirrors the switch in newWidget(). Aliases ("stocks") omitted.
var allWidgetTypes = []string{
	"bookmarks", "calendar", "change-detection", "clock", "custom-api",
	"dns-stats", "docker-containers", "extension", "group", "hacker-news",
	"html", "iframe", "lobsters", "markets", "monitor", "reddit",
	"releases", "repository", "rss", "search", "server-stats",
	"split-column", "to-do", "twitch-channels", "twitch-top-games",
	"videos", "weather",
}

// widgetFieldReferences caches the marshaled YAML of an empty instance of
// each widget type, used to show users what fields are available when
// editing. Computed once at startup.
var widgetFieldReferences = func() map[string]string {
	out := make(map[string]string, len(allWidgetTypes))
	for _, t := range allWidgetTypes {
		w, err := newWidget(t)
		if err != nil {
			continue
		}
		b, err := yaml.Marshal(w)
		if err != nil {
			continue
		}
		out[t] = string(b)
	}
	return out
}()

// widgetDocsAnchors maps a widget type to its anchor in upstream Glance docs.
// Anchors are GitHub-rendered from the headings in docs/configuration.md.
var widgetDocsAnchors = map[string]string{
	"bookmarks":         "bookmarks",
	"calendar":          "calendar",
	"calendar-legacy":   "calendar-legacy",
	"change-detection":  "changedetectionio",
	"clock":             "clock",
	"custom-api":        "custom-api",
	"dns-stats":         "dns-stats",
	"docker-containers": "docker-containers",
	"extension":         "extension",
	"group":             "group",
	"hacker-news":       "hacker-news",
	"lobsters":          "lobsters",
	"markets":           "markets",
	"monitor":           "monitor",
	"reddit":            "reddit",
	"releases":          "releases",
	"repository":        "repository",
	"rss":               "rss",
	"search":            "search-widget",
	"server-stats":      "server-stats",
	"split-column":      "split-column",
	"to-do":             "todo",
	"twitch-channels":   "twitch-channels",
	"twitch-top-games":  "twitch-top-games",
	"videos":            "videos",
	"weather":           "weather",
}

func widgetDocsURL(widgetType string) string {
	anchor, ok := widgetDocsAnchors[widgetType]
	if !ok {
		return "https://github.com/glanceapp/glance/blob/main/docs/configuration.md#widgets"
	}
	return "https://github.com/glanceapp/glance/blob/main/docs/configuration.md#" + anchor
}

// widgetStarters provides hand-curated minimal-but-meaningful YAML for each
// widget type, drawn from the upstream docs. Used to pre-fill the editor
// when adding a new widget so users see a working shape they can adapt.
var widgetStarters = map[string]string{
	"bookmarks": `type: bookmarks
groups:
  - title: Sites
    links:
      - title: Glance
        url: https://github.com/glanceapp/glance
`,
	"calendar":         "type: calendar\n",
	"calendar-legacy":  "type: calendar-legacy\n",
	"change-detection": `type: change-detection
instance-url: https://changedetection.example.com/
token: ${CHANGE_DETECTION_TOKEN}
`,
	"clock": "type: clock\n",
	"custom-api": `type: custom-api
url: https://api.example.com/data
template: |
  <p>Replace with a Go template that renders the JSON response.</p>
`,
	"dns-stats": `type: dns-stats
service: pihole
url: http://pi.hole
allow-insecure: true
username: admin
password: ${DNS_PASSWORD}
`,
	"docker-containers": "type: docker-containers\n",
	"extension": `type: extension
url: https://example.com/extension
`,
	"group": `type: group
widgets:
  - type: hacker-news
  - type: lobsters
`,
	"hacker-news": "type: hacker-news\n",
	"html": `type: html
source: |
  <p>Hello from a custom HTML widget.</p>
`,
	"iframe": `type: iframe
source: https://example.com
`,
	"lobsters": "type: lobsters\n",
	"markets": `type: markets
markets:
  - symbol: SPY
    name: S&P 500
  - symbol: BTC-USD
    name: Bitcoin
`,
	"monitor": `type: monitor
title: Services
sites:
  - title: Example
    url: https://example.com
`,
	"reddit": `type: reddit
subreddit: technology
`,
	"releases": `type: releases
repositories:
  - glanceapp/glance
`,
	"repository": `type: repository
repository: glanceapp/glance
`,
	"rss": `type: rss
feeds:
  - url: https://www.theverge.com/rss/index.xml
    title: The Verge
`,
	"search":       "type: search\n",
	"server-stats": "type: server-stats\n",
	"split-column": `type: split-column
widgets:
  - type: clock
  - type: search
`,
	"to-do": "type: to-do\n",
	"twitch-channels": `type: twitch-channels
channels:
  - shroud
`,
	"twitch-top-games": "type: twitch-top-games\n",
	"videos": `type: videos
channels:
  - UCXuqSBlHAE6Xw-yeJA0Tunw
`,
	"weather": `type: weather
location: London, GB
`,
}

func widgetStarterYAML(widgetType string) string {
	if s, ok := widgetStarters[widgetType]; ok {
		return s
	}
	return "type: " + widgetType + "\n"
}

type adminWidgetView struct {
	ColumnIndex int
	WidgetIndex int
	ID          uint64
	Title       string
	Type        string
	IsFirst     bool
	IsLast      bool
}

type adminColumnView struct {
	Index   int
	Size    string
	Widgets []adminWidgetView
}

type adminPageSummary struct {
	Title       string
	Slug        string
	WidgetCount int
	IsFirst     bool
	IsLast      bool
}

type adminBackupSummary struct {
	N       int
	Time    string // human-readable, e.g. "2 minutes ago"
	SizeKB  int
	Present bool
}

type adminPageDetail struct {
	Title       string
	Slug        string
	HeadWidgets []adminWidgetView
	Columns     []adminColumnView
}

type adminPageSettings struct {
	Name                   string
	Slug                   string
	Width                  string
	DesktopNavigationWidth string
	ShowMobileHeader       bool
	HideDesktopNavigation  bool
	CenterVertically       bool
}

type adminTemplateData struct {
	App     *application
	Request templateRequestData

	Pages          []adminPageSummary
	Page           *adminPageDetail
	Widget         *adminWidgetView
	WidgetTypes    []string
	WidgetYAML     string
	LoadError      string
	FieldReference string
	WidgetExample  string
	DocsURL        string
	IsNew          bool
	ErrorMessage   string

	PageSettings *adminPageSettings
	IsFirstPage  bool
	IsLastPage   bool

	SiteSettings  *adminSiteSettings
	ThemeSettings *adminThemeSettings

	Backups []adminBackupSummary
}

type adminThemeSettings struct {
	BackgroundColorHex       string
	PrimaryColorHex          string
	PositiveColorHex         string
	NegativeColorHex         string
	Light                    bool
	DisablePicker            bool
	ContrastMultiplier       float32
	TextSaturationMultiplier float32
	CustomCSSFile            string
}

type adminSiteSettings struct {
	AppName            string
	LogoText           string
	LogoURL            string
	FaviconURL         string
	AppIconURL         string
	AppBackgroundColor string
	HideFooter         bool
	CustomFooter       string
}

func (a *application) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}

	pages := a.freshPagesFromDisk()
	last := len(pages) - 1
	summaries := make([]adminPageSummary, 0, len(pages))
	for p := range pages {
		page := &pages[p]
		count := len(page.HeadWidgets)
		for c := range page.Columns {
			count += len(page.Columns[c].Widgets)
		}
		summaries = append(summaries, adminPageSummary{
			Title:       page.Title,
			Slug:        page.Slug,
			WidgetCount: count,
			IsFirst:     p == 0,
			IsLast:      p == last,
		})
	}

	data := adminTemplateData{App: a, Pages: summaries, Backups: a.collectBackups()}
	a.populateTemplateRequestData(&data.Request, r)
	renderAdminTemplate(w, adminIndexTemplate, data)
}

func (a *application) collectBackups() []adminBackupSummary {
	out := make([]adminBackupSummary, 0, maxBackups)
	now := time.Now()
	for n := 1; n <= maxBackups; n++ {
		info, err := os.Stat(backupPath(a.ConfigPath, n))
		if err != nil {
			out = append(out, adminBackupSummary{N: n, Present: false})
			continue
		}
		out = append(out, adminBackupSummary{
			N:       n,
			Time:    humanizeDuration(now.Sub(info.ModTime())) + " ago",
			SizeKB:  int(info.Size() / 1024),
			Present: true,
		})
	}
	return out
}

func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (a *application) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}

	pages := a.freshPagesFromDisk()
	page, _, exists := findPageBySlug(pages, r.PathValue("page"))
	if !exists {
		a.handleNotFound(w, r)
		return
	}

	detail := adminPageDetail{Title: page.Title, Slug: page.Slug}

	for i, widget := range page.HeadWidgets {
		detail.HeadWidgets = append(detail.HeadWidgets, adminWidgetView{
			ColumnIndex: -1,
			WidgetIndex: i,
			ID:          widget.GetID(),
			Title:       widget.GetTitle(),
			Type:        widget.GetType(),
		})
	}

	for c := range page.Columns {
		col := &page.Columns[c]
		view := adminColumnView{Index: c, Size: col.Size}
		last := len(col.Widgets) - 1
		for w, widget := range col.Widgets {
			view.Widgets = append(view.Widgets, adminWidgetView{
				ColumnIndex: c,
				WidgetIndex: w,
				ID:          widget.GetID(),
				Title:       widget.GetTitle(),
				Type:        widget.GetType(),
				IsFirst:     w == 0,
				IsLast:      w == last,
			})
		}
		detail.Columns = append(detail.Columns, view)
	}

	data := adminTemplateData{App: a, Page: &detail, WidgetTypes: allWidgetTypes}
	a.populateTemplateRequestData(&data.Request, r)
	renderAdminTemplate(w, adminPageTemplate, data)
}

func (a *application) handleAdminWidget(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}

	pages := a.freshPagesFromDisk()
	page, pageIdx, exists := findPageBySlug(pages, r.PathValue("page"))
	if !exists {
		a.handleNotFound(w, r)
		return
	}

	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		a.handleNotFound(w, r)
		return
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		a.handleNotFound(w, r)
		return
	}

	var widget widget
	if col == -1 {
		if idx < 0 || idx >= len(page.HeadWidgets) {
			a.handleNotFound(w, r)
			return
		}
		widget = page.HeadWidgets[idx]
	} else {
		if col < 0 || col >= len(page.Columns) {
			a.handleNotFound(w, r)
			return
		}
		column := &page.Columns[col]
		if idx < 0 || idx >= len(column.Widgets) {
			a.handleNotFound(w, r)
			return
		}
		widget = column.Widgets[idx]
	}

	yamlText, loadErr := loadWidgetYAML(a.ConfigPath, pageIdx, col, idx)

	a.renderWidgetEditor(w, r, editorRenderInput{
		pageTitle:   page.Title,
		pageSlug:    page.Slug,
		col:         col,
		idx:         idx,
		widgetType:  widget.GetType(),
		widgetTitle: widget.GetTitle(),
		yamlText:    yamlText,
		loadError:   loadErr,
	})
}

func hexOf(c *hslColorField) string {
	if c == nil {
		return ""
	}
	return c.ToHex()
}

func (a *application) handleAdminThemeSettings(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	t := a.Config.Theme
	settings := &adminThemeSettings{
		BackgroundColorHex:       hexOf(t.BackgroundColor),
		PrimaryColorHex:          hexOf(t.PrimaryColor),
		PositiveColorHex:         hexOf(t.PositiveColor),
		NegativeColorHex:         hexOf(t.NegativeColor),
		Light:                    t.Light,
		DisablePicker:            t.DisablePicker,
		ContrastMultiplier:       t.ContrastMultiplier,
		TextSaturationMultiplier: t.TextSaturationMultiplier,
		CustomCSSFile:            t.CustomCSSFile,
	}
	data := adminTemplateData{App: a, ThemeSettings: settings}
	a.populateTemplateRequestData(&data.Request, r)
	renderAdminTemplate(w, adminThemeSettingsTemplate, data)
}

func (a *application) handleAdminSiteSettings(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	b := a.Config.Branding
	settings := &adminSiteSettings{
		AppName:            b.AppName,
		LogoText:           b.LogoText,
		LogoURL:            b.LogoURL,
		FaviconURL:         b.FaviconURL,
		AppIconURL:         b.AppIconURL,
		AppBackgroundColor: b.AppBackgroundColor,
		HideFooter:         b.HideFooter,
		CustomFooter:       string(b.CustomFooter),
	}
	data := adminTemplateData{App: a, SiteSettings: settings}
	a.populateTemplateRequestData(&data.Request, r)
	renderAdminTemplate(w, adminSiteSettingsTemplate, data)
}

func (a *application) handleAdminPageSettings(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	pages := a.freshPagesFromDisk()
	page, idx, exists := findPageBySlug(pages, r.PathValue("page"))
	if !exists {
		a.handleNotFound(w, r)
		return
	}
	settings := &adminPageSettings{
		Name:                   page.Title,
		Slug:                   page.Slug,
		Width:                  page.Width,
		DesktopNavigationWidth: page.DesktopNavigationWidth,
		ShowMobileHeader:       page.ShowMobileHeader,
		HideDesktopNavigation:  page.HideDesktopNavigation,
		CenterVertically:       page.CenterVertically,
	}
	data := adminTemplateData{
		App:          a,
		Page:         &adminPageDetail{Title: page.Title, Slug: page.Slug},
		PageSettings: settings,
		IsFirstPage:  idx == 0,
		IsLastPage:   idx == len(pages)-1,
	}
	a.populateTemplateRequestData(&data.Request, r)
	renderAdminTemplate(w, adminPageSettingsTemplate, data)
}

func (a *application) handleAdminNewWidget(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}

	pages := a.freshPagesFromDisk()
	page, _, exists := findPageBySlug(pages, r.PathValue("page"))
	if !exists {
		a.handleNotFound(w, r)
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil || col < 0 || col >= len(page.Columns) {
		a.handleNotFound(w, r)
		return
	}
	widgetType := r.URL.Query().Get("type")
	if _, err := newWidget(widgetType); err != nil {
		adminError(w, http.StatusBadRequest, "unknown widget type: "+widgetType)
		return
	}

	a.renderWidgetEditor(w, r, editorRenderInput{
		pageTitle:  page.Title,
		pageSlug:   page.Slug,
		isNew:      true,
		col:        col,
		widgetType: widgetType,
		yamlText:   widgetStarterYAML(widgetType),
	})
}

type editorRenderInput struct {
	pageTitle    string
	pageSlug     string
	isNew        bool
	col          int
	idx          int
	widgetType   string
	widgetTitle  string
	yamlText     string
	loadError    string
	errorMessage string
}

func (a *application) renderWidgetEditor(w http.ResponseWriter, r *http.Request, in editorRenderInput) {
	view := adminWidgetView{
		ColumnIndex: in.col,
		WidgetIndex: in.idx,
		Title:       in.widgetTitle,
		Type:        in.widgetType,
	}
	detail := adminPageDetail{Title: in.pageTitle, Slug: in.pageSlug}

	data := adminTemplateData{
		App:            a,
		Page:           &detail,
		Widget:         &view,
		IsNew:          in.isNew,
		WidgetYAML:     in.yamlText,
		LoadError:      in.loadError,
		ErrorMessage:   in.errorMessage,
		FieldReference: widgetFieldReferences[in.widgetType],
		WidgetExample:  widgetStarters[in.widgetType],
		DocsURL:        widgetDocsURL(in.widgetType),
	}
	a.populateTemplateRequestData(&data.Request, r)
	renderAdminTemplate(w, adminWidgetTemplate, data)
}

// widgetTypeFromYAML extracts just the `type:` field from a YAML mapping.
// Used when re-rendering the editor after a save error so docs/reference
// match what the user is actually editing.
func widgetTypeFromYAML(yamlText string) string {
	var probe struct {
		Type string `yaml:"type"`
	}
	_ = yaml.Unmarshal([]byte(yamlText), &probe)
	return probe.Type
}

// freshPagesFromDisk reads the on-disk config and returns the parsed pages
// with slugs auto-generated. Admin views use this instead of a.Config.Pages
// because the fsnotify watcher debounces 500ms after a save, and during that
// window a.Config still reflects the pre-save state. Falls back to a copy
// of the cached config on any error.
func (a *application) freshPagesFromDisk() []page {
	contents, _, err := parseYAMLIncludes(a.ConfigPath)
	if err != nil {
		return a.Config.Pages
	}
	cfg, err := newConfigFromYAML(contents)
	if err != nil {
		return a.Config.Pages
	}
	for p := range cfg.Pages {
		if cfg.Pages[p].Slug == "" {
			cfg.Pages[p].Slug = titleToSlug(cfg.Pages[p].Title)
		}
	}
	return cfg.Pages
}

func findPageBySlug(pages []page, slug string) (*page, int, bool) {
	for i := range pages {
		if pages[i].Slug == slug {
			return &pages[i], i, true
		}
	}
	return nil, -1, false
}

// freshPageIndexBySlug looks up the on-disk page index by slug. Mutating
// handlers must use this rather than a.pageIndexBySlug because the cached
// a.Config can lag the file by up to ~500ms after a save.
func (a *application) freshPageIndexBySlug(slug string) (int, bool) {
	_, idx, ok := findPageBySlug(a.freshPagesFromDisk(), slug)
	return idx, ok
}

// loadWidgetYAML reads the on-disk config and returns the marshaled YAML for
// one widget. Errors are returned as a string so the template can show them
// without preventing the page from rendering.
func loadWidgetYAML(path string, pageIdx, col, idx int) (string, string) {
	editor, err := loadConfigEditor(path)
	if err != nil {
		return "", err.Error()
	}
	seq, pos, err := editor.widgetSlot(pageIdx, col, idx)
	if err != nil {
		return "", err.Error()
	}
	out, err := yaml.Marshal(seq.Content[pos])
	if err != nil {
		return "", err.Error()
	}
	return string(out), ""
}

// adminAccessAllowed enforces that admin endpoints either require auth, or
// have been explicitly opted into without auth via config.admin.allow-without-auth.
// Returns false if the response has been written and the caller should bail.
func (a *application) adminAccessAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !a.RequiresAuth && !a.Config.Admin.AllowWithoutAuth {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<!doctype html><meta charset=utf-8><title>Editing disabled</title>` +
			`<body style="font-family:sans-serif;padding:2rem;max-width:50rem;line-height:1.5">` +
			`<h1>Editing is disabled</h1>` +
			`<p>The edit UI requires authentication. Either:</p>` +
			`<ul>` +
			`<li>Configure <code>auth.users</code> in your config (recommended), or</li>` +
			`<li>Set <code>admin.allow-without-auth: true</code> in your config if you accept that anyone reachable on the network can edit it. (The legacy <code>admin</code> key still works for this option.)</li>` +
			`</ul>` +
			`</body>`))
		return false
	}
	if a.handleUnauthorizedResponse(w, r, redirectToLogin) {
		return false
	}
	return true
}

func renderAdminTemplate(w http.ResponseWriter, t *template.Template, data adminTemplateData) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	w.Write(buf.Bytes())
}
