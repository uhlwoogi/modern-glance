package glance

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// configEditor manipulates glance.yml as a yaml.Node tree, preserving
// comments, ordering, and unresolved ${env:X}/${secret:X} tokens that
// would be destroyed by round-tripping through the parsed config struct.
type configEditor struct {
	path string
	root yaml.Node
}

func loadConfigEditor(path string) (*configEditor, error) {
	_, includes, err := parseYAMLIncludes(path)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if len(includes) > 0 {
		return nil, fmt.Errorf("admin editing is not supported when the config uses include directives — edit included files manually for now")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	e := &configEditor{path: path}
	if err := yaml.Unmarshal(raw, &e.root); err != nil {
		return nil, fmt.Errorf("parsing config yaml: %w", err)
	}
	if e.root.Kind != yaml.DocumentNode || len(e.root.Content) == 0 {
		return nil, fmt.Errorf("config has no document content")
	}
	if e.root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config top-level is not a mapping")
	}
	return e, nil
}

const maxBackups = 10

func backupPath(configPath string, n int) string {
	return fmt.Sprintf("%s.bak.%d", configPath, n)
}

// rotateBackups shifts every .bak.N up by one (oldest gets dropped) so
// .bak.1 becomes free to receive the just-superseded contents.
func rotateBackups(configPath string) {
	// Drop the oldest first.
	os.Remove(backupPath(configPath, maxBackups))
	for i := maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(backupPath(configPath, i), backupPath(configPath, i+1))
	}
}

// save validates the edited tree against newConfigFromYAML, rotates the
// numbered backup chain (up to maxBackups versions), then writes via
// temp+rename for atomicity.
func (e *configEditor) save() error {
	out, err := yaml.Marshal(&e.root)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if _, err := newConfigFromYAML(out); err != nil {
		return fmt.Errorf("edited config is invalid: %w", err)
	}

	if existing, err := os.ReadFile(e.path); err == nil {
		rotateBackups(e.path)
		if werr := os.WriteFile(backupPath(e.path, 1), existing, 0644); werr != nil {
			log.Printf("admin: failed to write backup: %v", werr)
		}
	}

	tmpPath := e.path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, e.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file into place: %w", err)
	}
	return nil
}

func (e *configEditor) topMapping() *yaml.Node {
	return e.root.Content[0]
}

// findOrCreateKey returns the value node for the given key under a mapping.
// If create is true and the key does not exist, it is appended with a fresh
// value node of the given kind.
func findOrCreateKey(m *yaml.Node, key string, create bool, valueKind yaml.Kind) (*yaml.Node, error) {
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("node is not a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1], nil
		}
	}
	if !create {
		return nil, fmt.Errorf("key %q not found", key)
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valNode := &yaml.Node{Kind: valueKind}
	m.Content = append(m.Content, keyNode, valNode)
	return valNode, nil
}

func (e *configEditor) pagesNode(create bool) (*yaml.Node, error) {
	return findOrCreateKey(e.topMapping(), "pages", create, yaml.SequenceNode)
}

// widgetSlot returns the sequence node holding the widget and the widget's
// position within it. Caller can read via seq.Content[pos] or replace by
// assigning to seq.Content[pos]. Use colIdx = -1 for head widgets.
func (e *configEditor) widgetSlot(pageIdx, colIdx, widgetIdx int) (*yaml.Node, int, error) {
	pageNode, err := e.pageNodeAt(pageIdx)
	if err != nil {
		return nil, 0, err
	}

	var seq *yaml.Node
	if colIdx == -1 {
		seq, err = findOrCreateKey(pageNode, "head-widgets", false, yaml.SequenceNode)
		if err != nil {
			return nil, 0, err
		}
	} else {
		columns, err := columnsOf(pageNode)
		if err != nil {
			return nil, 0, err
		}
		if colIdx < 0 || colIdx >= len(columns.Content) {
			return nil, 0, fmt.Errorf("column index out of range")
		}
		seq, err = widgetsOf(columns.Content[colIdx])
		if err != nil {
			return nil, 0, err
		}
	}

	if widgetIdx < 0 || widgetIdx >= len(seq.Content) {
		return nil, 0, fmt.Errorf("widget index out of range")
	}
	return seq, widgetIdx, nil
}

func (e *configEditor) pageNodeAt(idx int) (*yaml.Node, error) {
	pages, err := e.pagesNode(false)
	if err != nil {
		return nil, err
	}
	if pages.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("pages is not a sequence")
	}
	if idx < 0 || idx >= len(pages.Content) {
		return nil, fmt.Errorf("page index %d out of range", idx)
	}
	return pages.Content[idx], nil
}

func columnsOf(pageNode *yaml.Node) (*yaml.Node, error) {
	return findOrCreateKey(pageNode, "columns", false, yaml.SequenceNode)
}

func widgetsOf(columnNode *yaml.Node) (*yaml.Node, error) {
	return findOrCreateKey(columnNode, "widgets", true, yaml.SequenceNode)
}

func newColumnNode(size string) *yaml.Node {
	if size == "" {
		size = "full"
	}
	return &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "size"},
		{Kind: yaml.ScalarNode, Value: size},
		{Kind: yaml.ScalarNode, Value: "widgets"},
		{Kind: yaml.SequenceNode},
	}}
}

func setColumnSize(columnNode *yaml.Node, size string) error {
	if columnNode.Kind != yaml.MappingNode {
		return fmt.Errorf("column is not a mapping")
	}
	for i := 0; i+1 < len(columnNode.Content); i += 2 {
		if columnNode.Content[i].Value == "size" {
			columnNode.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: size}
			return nil
		}
	}
	columnNode.Content = append(columnNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "size"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: size},
	)
	return nil
}

func newPageNode(title string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "name"},
			{Kind: yaml.ScalarNode, Value: title},
			{Kind: yaml.ScalarNode, Value: "columns"},
			{Kind: yaml.SequenceNode, Content: []*yaml.Node{
				{Kind: yaml.MappingNode, Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Value: "size"},
					{Kind: yaml.ScalarNode, Value: "full"},
					{Kind: yaml.ScalarNode, Value: "widgets"},
					{Kind: yaml.SequenceNode},
				}},
			}},
		},
	}
}

// adminError renders a minimal HTML error page so failed mutations don't
// leave the user staring at a blank screen. The hot-reload watcher will
// still pick up any successful intermediate save state.
func adminError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte("<!doctype html><meta charset=utf-8><title>Edit error</title><body style=\"font-family:sans-serif;padding:2rem;max-width:50rem\"><h1>Couldn't save</h1><p>The change wasn't applied. Your config file is unchanged.</p><pre style=\"white-space:pre-wrap;background:#f4f4f4;padding:1rem;border-radius:4px\">"))
	w.Write([]byte(htmlEscape(msg)))
	w.Write([]byte("</pre><p><a href=\"javascript:history.back()\">Back</a></p>"))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// ---- handlers ----

func (a *application) handleAdminAddPage(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		adminError(w, http.StatusBadRequest, "page title is required")
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pages, err := editor.pagesNode(true)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pages.Content = append(pages.Content, newPageNode(title))

	if err := editor.save(); err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit", http.StatusSeeOther)
}

func (a *application) handleAdminDeletePage(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	idx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		a.handleNotFound(w, r)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pages, err := editor.pagesNode(false)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if idx >= len(pages.Content) {
		adminError(w, http.StatusInternalServerError, "page index out of range — config may have been edited externally")
		return
	}
	if len(pages.Content) <= 1 {
		adminError(w, http.StatusBadRequest, "cannot delete the last remaining page")
		return
	}
	pages.Content = append(pages.Content[:idx], pages.Content[idx+1:]...)

	if err := editor.save(); err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit", http.StatusSeeOther)
}

func (a *application) handleAdminDeleteWidget(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
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

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	columns, err := columnsOf(pageNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if col < 0 || col >= len(columns.Content) {
		adminError(w, http.StatusBadRequest, "column index out of range")
		return
	}
	widgets, err := widgetsOf(columns.Content[col])
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if idx < 0 || idx >= len(widgets.Content) {
		adminError(w, http.StatusBadRequest, "widget index out of range")
		return
	}
	widgets.Content = append(widgets.Content[:idx], widgets.Content[idx+1:]...)

	if err := editor.save(); err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/pages/"+slug, http.StatusSeeOther)
}

func (a *application) handleAdminMoveWidget(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
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

	dir := r.URL.Query().Get("dir")
	delta := 0
	switch dir {
	case "up":
		delta = -1
	case "down":
		delta = 1
	default:
		adminError(w, http.StatusBadRequest, "dir must be 'up' or 'down'")
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	columns, err := columnsOf(pageNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if col < 0 || col >= len(columns.Content) {
		adminError(w, http.StatusBadRequest, "column index out of range")
		return
	}
	widgets, err := widgetsOf(columns.Content[col])
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	target := idx + delta
	if idx < 0 || idx >= len(widgets.Content) || target < 0 || target >= len(widgets.Content) {
		adminError(w, http.StatusBadRequest, "cannot move further in that direction")
		return
	}
	widgets.Content[idx], widgets.Content[target] = widgets.Content[target], widgets.Content[idx]

	if err := editor.save(); err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/pages/"+slug, http.StatusSeeOther)
}

func (a *application) handleAdminEditWidget(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	slug := r.PathValue("page")
	page, pageIdx, exists := findPageBySlug(a.freshPagesFromDisk(), slug)
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

	yamlText := r.FormValue("yaml")

	rerenderError := func(msg string) {
		a.renderWidgetEditor(w, r, editorRenderInput{
			pageTitle:    page.Title,
			pageSlug:     page.Slug,
			col:          col,
			idx:          idx,
			widgetType:   widgetTypeFromYAML(yamlText),
			yamlText:     yamlText,
			errorMessage: msg,
		})
	}

	newNode, err := parseWidgetYAML(yamlText)
	if err != nil {
		rerenderError(err.Error())
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		rerenderError(err.Error())
		return
	}
	seq, pos, err := editor.widgetSlot(pageIdx, col, idx)
	if err != nil {
		rerenderError(err.Error())
		return
	}
	seq.Content[pos] = newNode

	if err := editor.save(); err != nil {
		rerenderError(err.Error())
		return
	}
	http.Redirect(w, r,
		fmt.Sprintf("%s/edit/pages/%s/widgets/%d/%d", a.Config.Server.BaseURL, slug, col, idx),
		http.StatusSeeOther)
}

// parseWidgetYAML parses the editor's submitted text into a single mapping
// node suitable for splicing into the config tree.
func parseWidgetYAML(yamlText string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("submitted YAML is empty")
	}
	node := doc.Content[0]
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("widget YAML must be a mapping (key: value pairs)")
	}
	return node, nil
}

func (a *application) handleAdminCreateWidget(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	slug := r.PathValue("page")
	page, pageIdx, exists := findPageBySlug(a.freshPagesFromDisk(), slug)
	if !exists {
		a.handleNotFound(w, r)
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		a.handleNotFound(w, r)
		return
	}

	yamlText := r.FormValue("yaml")

	rerenderError := func(msg string) {
		a.renderWidgetEditor(w, r, editorRenderInput{
			pageTitle:    page.Title,
			pageSlug:     page.Slug,
			isNew:        true,
			col:          col,
			widgetType:   widgetTypeFromYAML(yamlText),
			yamlText:     yamlText,
			errorMessage: msg,
		})
	}

	newNode, err := parseWidgetYAML(yamlText)
	if err != nil {
		rerenderError(err.Error())
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		rerenderError(err.Error())
		return
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		rerenderError(err.Error())
		return
	}
	columns, err := columnsOf(pageNode)
	if err != nil {
		rerenderError("page has no columns; add a column first")
		return
	}
	if col < 0 || col >= len(columns.Content) {
		rerenderError("column index out of range")
		return
	}
	widgets, err := widgetsOf(columns.Content[col])
	if err != nil {
		rerenderError(err.Error())
		return
	}
	widgets.Content = append(widgets.Content, newNode)

	if err := editor.save(); err != nil {
		rerenderError(err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/pages/"+slug, http.StatusSeeOther)
}

// handleAdminLayout accepts a JSON description of a page's new column/widget
// layout and rewrites the yaml.Node tree by moving widget nodes between
// columns. Used by the dashboard edit-mode drag-drop UI. The request must
// reference each existing widget exactly once so we never lose or duplicate
// nodes. Edits to widget contents stay untouched — only positions move.
func (a *application) handleAdminLayout(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}

	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}

	type widgetRef struct {
		Col int `json:"col"`
		Idx int `json:"idx"`
	}
	var req struct {
		HeadWidgets []widgetRef   `json:"headWidgets"`
		Columns     [][]widgetRef `json:"columns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	columns, err := columnsOf(pageNode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(req.Columns) != len(columns.Content) {
		http.Error(w, fmt.Sprintf("layout has %d columns, page has %d", len(req.Columns), len(columns.Content)), http.StatusBadRequest)
		return
	}

	// Snapshot head widgets (col = -1). Optional — pages may not have any.
	var headWidgetsNode *yaml.Node
	var headSnapshot []*yaml.Node
	if hw, err := findOrCreateKey(pageNode, "head-widgets", false, yaml.SequenceNode); err == nil {
		headWidgetsNode = hw
		headSnapshot = make([]*yaml.Node, len(hw.Content))
		copy(headSnapshot, hw.Content)
	}
	if len(req.HeadWidgets) != len(headSnapshot) {
		http.Error(w, fmt.Sprintf("layout has %d head widgets, page has %d", len(req.HeadWidgets), len(headSnapshot)), http.StatusBadRequest)
		return
	}

	// Snapshot the existing widget nodes by [col][idx] so we can splice them
	// into the new layout without mutating during traversal.
	snapshot := make([][]*yaml.Node, len(columns.Content))
	totalCount := 0
	for c, colNode := range columns.Content {
		widgets, err := widgetsOf(colNode)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		snapshot[c] = make([]*yaml.Node, len(widgets.Content))
		copy(snapshot[c], widgets.Content)
		totalCount += len(widgets.Content)
	}

	// Validate: each widget referenced exactly once, all references in range.
	// Column widgets use col >= 0 keys; head widgets use col = -1.
	seen := make(map[[2]int]bool, totalCount+len(headSnapshot))
	checkRef := func(ref widgetRef) error {
		if ref.Col == -1 {
			if ref.Idx < 0 || ref.Idx >= len(headSnapshot) {
				return fmt.Errorf("invalid head widget reference idx=%d", ref.Idx)
			}
		} else {
			if ref.Col < 0 || ref.Col >= len(snapshot) || ref.Idx < 0 || ref.Idx >= len(snapshot[ref.Col]) {
				return fmt.Errorf("invalid widget reference col=%d idx=%d", ref.Col, ref.Idx)
			}
		}
		key := [2]int{ref.Col, ref.Idx}
		if seen[key] {
			return fmt.Errorf("widget col=%d idx=%d referenced more than once", ref.Col, ref.Idx)
		}
		seen[key] = true
		return nil
	}
	for _, ref := range req.HeadWidgets {
		if ref.Col != -1 {
			http.Error(w, "head widgets must use col=-1", http.StatusBadRequest)
			return
		}
		if err := checkRef(ref); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	refCount := 0
	for _, newCol := range req.Columns {
		for _, ref := range newCol {
			if ref.Col == -1 {
				http.Error(w, "head widgets cannot move into columns", http.StatusBadRequest)
				return
			}
			if err := checkRef(ref); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			refCount++
		}
	}
	if refCount != totalCount {
		http.Error(w, fmt.Sprintf("layout references %d column widgets but page has %d", refCount, totalCount), http.StatusBadRequest)
		return
	}

	// Apply: rebuild each column's widgets sequence from the snapshot.
	for c, newCol := range req.Columns {
		widgets, _ := widgetsOf(columns.Content[c])
		newContent := make([]*yaml.Node, 0, len(newCol))
		for _, ref := range newCol {
			newContent = append(newContent, snapshot[ref.Col][ref.Idx])
		}
		widgets.Content = newContent
	}
	// And rebuild head widgets sequence.
	if headWidgetsNode != nil {
		newHead := make([]*yaml.Node, 0, len(req.HeadWidgets))
		for _, ref := range req.HeadWidgets {
			newHead = append(newHead, headSnapshot[ref.Idx])
		}
		headWidgetsNode.Content = newHead
	}

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// jsonToYAMLNode converts the JSON-decoded value (string/float64/bool/nil/
// []any/map[string]any) into a yaml.Node so it can be spliced into the
// config tree. Numbers come in as float64 from encoding/json; we render them
// as ints when they have no fractional part (most widget fields are int).
func jsonToYAMLNode(v interface{}) (*yaml.Node, error) {
	switch x := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: x}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatBool(x)}, nil
	case float64:
		if x == float64(int64(x)) {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatInt(int64(x), 10)}, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatFloat(x, 'f', -1, 64)}, nil
	case []interface{}:
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range x {
			n, err := jsonToYAMLNode(item)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, n)
		}
		return seq, nil
	case map[string]interface{}:
		m := &yaml.Node{Kind: yaml.MappingNode}
		for k, vv := range x {
			if vv == nil {
				// Skip null fields rather than writing `title: null`.
				continue
			}
			n, err := jsonToYAMLNode(vv)
			if err != nil {
				return nil, err
			}
			m.Content = append(m.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				n,
			)
		}
		return m, nil
	}
	return nil, fmt.Errorf("unsupported JSON type %T", v)
}

// applyFieldsToWidget surgically updates a widget mapping node from a JSON
// fields map. Existing keys are replaced; new keys are appended. A null
// value removes the key. Other keys (and comments) on the widget are left
// untouched, so users can still hand-edit advanced fields the dialog
// doesn't know about.
func applyFieldsToWidget(widget *yaml.Node, fields map[string]interface{}) error {
	if widget.Kind != yaml.MappingNode {
		return fmt.Errorf("widget node is not a mapping")
	}

	for key, val := range fields {
		existing := -1
		for i := 0; i+1 < len(widget.Content); i += 2 {
			if widget.Content[i].Value == key {
				existing = i
				break
			}
		}

		if val == nil {
			if existing != -1 {
				widget.Content = append(widget.Content[:existing], widget.Content[existing+2:]...)
			}
			continue
		}

		valNode, err := jsonToYAMLNode(val)
		if err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
		if existing == -1 {
			widget.Content = append(widget.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: key},
				valNode,
			)
		} else {
			widget.Content[existing+1] = valNode
		}
	}
	return nil
}

// handleAdminUpdateFields applies JSON form values to an existing widget
// without touching its other YAML fields. Used by the inline edit dialog.
func (a *application) handleAdminUpdateFields(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		http.Error(w, "bad column", http.StatusBadRequest)
		return
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		http.Error(w, "bad index", http.StatusBadRequest)
		return
	}

	var fields map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	seq, pos, err := editor.widgetSlot(pageIdx, col, idx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := applyFieldsToWidget(seq.Content[pos], fields); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleAdminCreateFromFields creates a new widget at the end of a column
// from a JSON {type, fields} payload. The dialog uses this when the user
// clicks the "+" button in edit mode and fills in a form.
func (a *application) handleAdminCreateFromFields(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		http.Error(w, "bad column", http.StatusBadRequest)
		return
	}

	var req struct {
		Type   string                 `json:"type"`
		Fields map[string]interface{} `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		http.Error(w, "type is required", http.StatusBadRequest)
		return
	}
	if _, err := newWidget(req.Type); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	columns, err := columnsOf(pageNode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if col < 0 || col >= len(columns.Content) {
		http.Error(w, "column index out of range", http.StatusBadRequest)
		return
	}
	widgets, err := widgetsOf(columns.Content[col])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	widget := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "type"},
		{Kind: yaml.ScalarNode, Value: req.Type},
	}}
	if err := applyFieldsToWidget(widget, req.Fields); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	widgets.Content = append(widgets.Content, widget)

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleAdminGetFields returns the current values of one widget as JSON,
// drawn from the on-disk yaml.Node so unresolved ${env:X} tokens stay as
// the user typed them. The dialog form generator uses this to populate
// initial values when editing an existing widget.
func (a *application) handleAdminGetFields(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		http.Error(w, "bad column", http.StatusBadRequest)
		return
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		http.Error(w, "bad index", http.StatusBadRequest)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	seq, pos, err := editor.widgetSlot(pageIdx, col, idx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var fields map[string]interface{}
	if err := seq.Content[pos].Decode(&fields); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(fields); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleAdminWidgetSchemas exposes the widget field schemas as JSON for the
// edit-mode dialog. Cached client-side; no auth-related data here.
func (a *application) handleAdminWidgetSchemas(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(widgetSchemas); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---------- theme settings ----------

// hexToHSL converts a #rrggbb (or #rgb) string into HSL components matching
// Glance's hslColorField format (H 0-360, S/L 0-100).
func hexToHSL(hex string) (float64, float64, float64, error) {
	s := strings.TrimSpace(strings.TrimPrefix(hex, "#"))
	if len(s) == 3 {
		s = string(s[0]) + string(s[0]) + string(s[1]) + string(s[1]) + string(s[2]) + string(s[2])
	}
	if len(s) != 6 {
		return 0, 0, 0, fmt.Errorf("hex color must be #rgb or #rrggbb, got %q", hex)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parsing hex %q: %w", hex, err)
	}
	r := float64((v>>16)&0xff) / 255.0
	g := float64((v>>8)&0xff) / 255.0
	b := float64(v&0xff) / 255.0

	maxC, minC := r, r
	if g > maxC {
		maxC = g
	}
	if b > maxC {
		maxC = b
	}
	if g < minC {
		minC = g
	}
	if b < minC {
		minC = b
	}
	delta := maxC - minC
	l := (maxC + minC) / 2

	var h, sat float64
	if delta == 0 {
		h, sat = 0, 0
	} else {
		if l < 0.5 {
			sat = delta / (maxC + minC)
		} else {
			sat = delta / (2 - maxC - minC)
		}
		switch maxC {
		case r:
			h = (g - b) / delta
			if g < b {
				h += 6
			}
		case g:
			h = (b-r)/delta + 2
		case b:
			h = (r-g)/delta + 4
		}
		h *= 60
	}
	// Round to nearest int for tidy YAML.
	return float64(int(h + 0.5)), float64(int(sat*100 + 0.5)), float64(int(l*100 + 0.5)), nil
}

// hslToYAMLString returns the canonical Glance HSL representation: "H S L".
func hslToYAMLString(h, s, l float64) string {
	return fmt.Sprintf("%g %g %g", h, s, l)
}

var themeBoolKeys = map[string]bool{"light": true, "disable-picker": true}
var themeNumberKeys = map[string]bool{"contrast-multiplier": true, "text-saturation-multiplier": true}
var themeColorKeys = map[string]bool{"background-color": true, "primary-color": true, "positive-color": true, "negative-color": true}

func (a *application) handleAdminUpdateTheme(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	theme, err := findOrCreateKey(editor.topMapping(), "theme", true, yaml.MappingNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fields := make(map[string]interface{})
	// Colors: form sends hex, we store HSL.
	for key := range themeColorKeys {
		raw := strings.TrimSpace(r.FormValue(key))
		if raw == "" {
			fields[key] = nil
			continue
		}
		h, s, l, err := hexToHSL(raw)
		if err != nil {
			adminError(w, http.StatusBadRequest, err.Error())
			return
		}
		fields[key] = hslToYAMLString(h, s, l)
	}
	// Numbers
	for key := range themeNumberKeys {
		raw := strings.TrimSpace(r.FormValue(key))
		if raw == "" {
			fields[key] = nil
			continue
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			adminError(w, http.StatusBadRequest, fmt.Sprintf("%s: %v", key, err))
			return
		}
		fields[key] = f
	}
	// Booleans (browsers omit unchecked, so default to false).
	for key := range themeBoolKeys {
		raw := strings.TrimSpace(r.FormValue(key))
		fields[key] = raw == "on" || raw == "true" || raw == "1"
	}
	// String paths
	if r.Form.Has("custom-css-file") {
		raw := strings.TrimSpace(r.FormValue("custom-css-file"))
		if raw == "" {
			fields["custom-css-file"] = nil
		} else {
			fields["custom-css-file"] = raw
		}
	}

	if err := applyFieldsToWidget(theme, fields); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := editor.save(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/theme-settings", http.StatusSeeOther)
}

// ---------- theme presets ----------

func removeKeyFromMapping(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func (a *application) handleAdminCreateOrUpdatePreset(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		key = strings.TrimSpace(r.FormValue("name"))
	}
	if key == "" {
		adminError(w, http.StatusBadRequest, "preset name is required")
		return
	}
	for _, ch := range key {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == ':' || ch == '{' || ch == '}' || ch == '[' || ch == ']' || ch == '|' || ch == '>' || ch == '&' || ch == '*' {
			adminError(w, http.StatusBadRequest, "preset name must not contain spaces or special characters")
			return
		}
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	theme, err := findOrCreateKey(editor.topMapping(), "theme", true, yaml.MappingNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	presets, err := findOrCreateKey(theme, "presets", true, yaml.MappingNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	preset, err := findOrCreateKey(presets, key, true, yaml.MappingNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fields := make(map[string]interface{})
	for fkey := range themeColorKeys {
		raw := strings.TrimSpace(r.FormValue(fkey))
		if raw == "" {
			fields[fkey] = nil
			continue
		}
		h, s, l, err := hexToHSL(raw)
		if err != nil {
			adminError(w, http.StatusBadRequest, err.Error())
			return
		}
		fields[fkey] = hslToYAMLString(h, s, l)
	}
	for fkey := range themeNumberKeys {
		raw := strings.TrimSpace(r.FormValue(fkey))
		if raw == "" {
			fields[fkey] = nil
			continue
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			adminError(w, http.StatusBadRequest, fmt.Sprintf("%s: %v", fkey, err))
			return
		}
		fields[fkey] = f
	}
	raw := strings.TrimSpace(r.FormValue("light"))
	fields["light"] = raw == "on" || raw == "true" || raw == "1"

	if err := applyFieldsToWidget(preset, fields); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := editor.save(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/theme-settings", http.StatusSeeOther)
}

func (a *application) handleAdminDeletePreset(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	key := r.PathValue("key")

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	theme, err := findOrCreateKey(editor.topMapping(), "theme", false, yaml.MappingNode)
	if err != nil {
		http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/theme-settings", http.StatusSeeOther)
		return
	}
	presetsNode, err := findOrCreateKey(theme, "presets", false, yaml.MappingNode)
	if err != nil {
		http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/theme-settings", http.StatusSeeOther)
		return
	}
	removeKeyFromMapping(presetsNode, key)

	if err := editor.save(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/theme-settings", http.StatusSeeOther)
}

// ---------- site settings (branding) ----------

var brandingFieldKeys = []string{
	"hide-footer",
	"custom-footer",
	"logo-text",
	"logo-url",
	"favicon-url",
	"app-name",
	"app-icon-url",
	"app-background-color",
}

func (a *application) handleAdminUpdateSiteSettings(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	branding, err := findOrCreateKey(editor.topMapping(), "branding", true, yaml.MappingNode)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fields := make(map[string]interface{})
	for _, key := range brandingFieldKeys {
		if !r.Form.Has(key) {
			continue
		}
		raw := strings.TrimSpace(r.FormValue(key))
		if key == "hide-footer" {
			fields[key] = raw == "on" || raw == "true" || raw == "1"
			continue
		}
		if raw == "" {
			fields[key] = nil
		} else {
			fields[key] = raw
		}
	}
	// Force unchecked checkboxes to false (browsers omit them).
	if _, ok := fields["hide-footer"]; !ok {
		fields["hide-footer"] = false
	}

	if err := applyFieldsToWidget(branding, fields); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := editor.save(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit/site-settings", http.StatusSeeOther)
}

// ---------- restore from .bak ----------

// handleAdminRestore swaps the current config with .bak.1 (single-step undo).
// Click again to flip back. Older numbered backups stay untouched.
func (a *application) handleAdminRestore(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	bakPath := backupPath(a.ConfigPath, 1)
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		adminError(w, http.StatusBadRequest, "no backup to restore: "+err.Error())
		return
	}
	if _, err := newConfigFromYAML(bakContent); err != nil {
		adminError(w, http.StatusBadRequest, "backup is invalid: "+err.Error())
		return
	}
	currentContent, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(bakPath, currentContent, 0644); err != nil {
		adminError(w, http.StatusInternalServerError, "writing new backup: "+err.Error())
		return
	}
	tmpPath := a.ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, bakContent, 0644); err != nil {
		adminError(w, http.StatusInternalServerError, "writing temp file: "+err.Error())
		return
	}
	if err := os.Rename(tmpPath, a.ConfigPath); err != nil {
		os.Remove(tmpPath)
		adminError(w, http.StatusInternalServerError, "renaming temp file: "+err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit", http.StatusSeeOther)
}

// handleAdminRestoreFromBackup restores from a specific numbered backup. The
// just-current contents are saved as a fresh .bak.1 (rotating older ones up)
// so the user can always undo their undo.
func (a *application) handleAdminRestoreFromBackup(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 || n > maxBackups {
		http.Error(w, "bad backup number", http.StatusBadRequest)
		return
	}

	bakContent, err := os.ReadFile(backupPath(a.ConfigPath, n))
	if err != nil {
		adminError(w, http.StatusBadRequest, "backup not found: "+err.Error())
		return
	}
	if _, err := newConfigFromYAML(bakContent); err != nil {
		adminError(w, http.StatusBadRequest, "backup is invalid: "+err.Error())
		return
	}
	currentContent, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rotateBackups(a.ConfigPath)
	if err := os.WriteFile(backupPath(a.ConfigPath, 1), currentContent, 0644); err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := a.ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, bakContent, 0644); err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(tmpPath, a.ConfigPath); err != nil {
		os.Remove(tmpPath)
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit", http.StatusSeeOther)
}

// ---------- page settings + reorder ----------

// pageMetadataKeys is the set of page-level keys the settings form handles.
// Anything else (head-widgets, columns) stays untouched on save.
var pageMetadataKeys = map[string]bool{
	"name":                     true,
	"slug":                     true,
	"width":                    true,
	"desktop-navigation-width": true,
	"show-mobile-header":       true,
	"hide-desktop-navigation":  true,
	"center-vertically":        true,
}

func (a *application) handleAdminUpdatePageFields(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slug := r.PathValue("page")
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		a.handleNotFound(w, r)
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}

	fields := make(map[string]interface{})
	for key := range pageMetadataKeys {
		if !r.Form.Has(key) {
			continue
		}
		raw := strings.TrimSpace(r.FormValue(key))
		switch key {
		case "show-mobile-header", "hide-desktop-navigation", "center-vertically":
			fields[key] = raw == "on" || raw == "true" || raw == "1"
		default:
			if raw == "" {
				fields[key] = nil // remove the key from yaml
			} else {
				fields[key] = raw
			}
		}
	}
	// Browsers don't post unchecked checkboxes; explicitly set them false so
	// turning a flag off persists.
	for _, key := range []string{"show-mobile-header", "hide-desktop-navigation", "center-vertically"} {
		if _, set := fields[key]; !set {
			fields[key] = false
		}
	}

	if name, ok := fields["name"].(string); !ok || name == "" {
		adminError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := applyFieldsToWidget(pageNode, fields); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := editor.save(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The slug may have changed if the user edited it (or the title without an
	// explicit slug); redirect to the page list rather than a possibly-stale URL.
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit", http.StatusSeeOther)
}

func (a *application) handleAdminMovePage(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	slug := r.PathValue("page")
	idx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		a.handleNotFound(w, r)
		return
	}
	delta := 0
	switch r.URL.Query().Get("dir") {
	case "up":
		delta = -1
	case "down":
		delta = 1
	default:
		adminError(w, http.StatusBadRequest, "dir must be up|down")
		return
	}

	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pages, err := editor.pagesNode(false)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	target := idx + delta
	if idx < 0 || idx >= len(pages.Content) || target < 0 || target >= len(pages.Content) {
		adminError(w, http.StatusBadRequest, "cannot move further in that direction")
		return
	}
	pages.Content[idx], pages.Content[target] = pages.Content[target], pages.Content[idx]

	if err := editor.save(); err != nil {
		adminError(w, http.StatusBadRequest, err.Error())
		return
	}
	http.Redirect(w, r, a.Config.Server.BaseURL+"/edit", http.StatusSeeOther)
}

// ---------- column endpoints ----------

func (a *application) editColumns(slug string) (*configEditor, *yaml.Node, error) {
	pageIdx, ok := a.freshPageIndexBySlug(slug)
	if !ok {
		return nil, nil, fmt.Errorf("page not found")
	}
	editor, err := loadConfigEditor(a.ConfigPath)
	if err != nil {
		return nil, nil, err
	}
	pageNode, err := editor.pageNodeAt(pageIdx)
	if err != nil {
		return nil, nil, err
	}
	columns, err := columnsOf(pageNode)
	if err != nil {
		return nil, nil, err
	}
	return editor, columns, nil
}

func (a *application) handleAdminAddColumn(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	size := strings.TrimSpace(r.FormValue("size"))
	if size == "" {
		size = "full"
	}

	editor, columns, err := a.editColumns(r.PathValue("page"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	columns.Content = append(columns.Content, newColumnNode(size))

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *application) handleAdminDeleteColumn(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		http.Error(w, "bad column", http.StatusBadRequest)
		return
	}

	editor, columns, err := a.editColumns(r.PathValue("page"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if col < 0 || col >= len(columns.Content) {
		http.Error(w, "column index out of range", http.StatusBadRequest)
		return
	}
	if len(columns.Content) <= 1 {
		http.Error(w, "cannot delete the last remaining column", http.StatusBadRequest)
		return
	}
	columns.Content = append(columns.Content[:col], columns.Content[col+1:]...)

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *application) handleAdminMoveColumn(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		http.Error(w, "bad column", http.StatusBadRequest)
		return
	}
	dir := r.URL.Query().Get("dir")
	delta := 0
	switch dir {
	case "up", "left":
		delta = -1
	case "down", "right":
		delta = 1
	default:
		http.Error(w, "dir must be up|down|left|right", http.StatusBadRequest)
		return
	}

	editor, columns, err := a.editColumns(r.PathValue("page"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target := col + delta
	if col < 0 || col >= len(columns.Content) || target < 0 || target >= len(columns.Content) {
		http.Error(w, "cannot move further in that direction", http.StatusBadRequest)
		return
	}
	columns.Content[col], columns.Content[target] = columns.Content[target], columns.Content[col]

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (a *application) handleAdminColumnSize(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	col, err := strconv.Atoi(r.PathValue("col"))
	if err != nil {
		http.Error(w, "bad column", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	size := strings.TrimSpace(r.FormValue("size"))
	if size == "" {
		http.Error(w, "size is required", http.StatusBadRequest)
		return
	}

	editor, columns, err := a.editColumns(r.PathValue("page"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if col < 0 || col >= len(columns.Content) {
		http.Error(w, "column index out of range", http.StatusBadRequest)
		return
	}
	if err := setColumnSize(columns.Content[col], size); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := editor.save(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}
