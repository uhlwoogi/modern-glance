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

// save validates the edited tree against newConfigFromYAML, writes a
// .bak of the previous file, then writes via temp+rename for atomicity.
func (e *configEditor) save() error {
	out, err := yaml.Marshal(&e.root)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if _, err := newConfigFromYAML(out); err != nil {
		return fmt.Errorf("edited config is invalid: %w", err)
	}

	if existing, err := os.ReadFile(e.path); err == nil {
		if werr := os.WriteFile(e.path+".bak", existing, 0644); werr != nil {
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

	var req struct {
		Columns [][]struct {
			Col int `json:"col"`
			Idx int `json:"idx"`
		} `json:"columns"`
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
	seen := make(map[[2]int]bool, totalCount)
	refCount := 0
	for _, newCol := range req.Columns {
		for _, ref := range newCol {
			if ref.Col < 0 || ref.Col >= len(snapshot) || ref.Idx < 0 || ref.Idx >= len(snapshot[ref.Col]) {
				http.Error(w, fmt.Sprintf("invalid widget reference col=%d idx=%d", ref.Col, ref.Idx), http.StatusBadRequest)
				return
			}
			key := [2]int{ref.Col, ref.Idx}
			if seen[key] {
				http.Error(w, fmt.Sprintf("widget col=%d idx=%d referenced more than once", ref.Col, ref.Idx), http.StatusBadRequest)
				return
			}
			seen[key] = true
			refCount++
		}
	}
	if refCount != totalCount {
		http.Error(w, fmt.Sprintf("layout references %d widgets but page has %d", refCount, totalCount), http.StatusBadRequest)
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
