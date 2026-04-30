/* Edit mode for the dashboard.
 *
 * Toggle: #edit-mode-toggle. Adds drag/edit/delete handles per widget +
 * "+ Add widget" buttons per column. Drag-drop saves to /edit/api/.../layout.
 * Edit handle opens an inline form dialog driven by widget schema (falls
 * back to the YAML editor for unscheaded widget types). Delete handle posts
 * to /edit/api/.../delete. Persisted across reloads via localStorage.
 */

const STORAGE_KEY = "glance_edit_mode";

const state = {
    active: false,
    sortables: [],
    schemas: null, // cached schema map, keyed by widget type
};

function pageInfo() {
    return {
        slug: typeof pageData !== "undefined" ? pageData.slug || "" : "",
        baseURL: typeof pageData !== "undefined" ? pageData.baseURL || "" : "",
    };
}

function api(path) {
    return pageInfo().baseURL + path;
}

function escapeHTML(s) {
    return String(s ?? "").replace(/[&<>"']/g, (c) =>
        ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c],
    );
}

function cssEscape(s) {
    if (window.CSS && CSS.escape) return CSS.escape(s);
    return String(s).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
}

function widgetTypeOf(widget) {
    const m = String(widget.className || "").match(/widget-type-(\S+)/);
    return m ? m[1] : "";
}

/* ---------- Layout (drag-drop) ---------- */

function indexWidgets() {
    document.querySelectorAll(".head-widgets > .widget").forEach((w, idx) => {
        w.dataset.origCol = "-1";
        w.dataset.origIdx = String(idx);
    });
    document.querySelectorAll(".page-column").forEach((col, colIdx) => {
        col.dataset.colIdx = String(colIdx);
        col.querySelectorAll(":scope > .widget").forEach((w, idx) => {
            w.dataset.origCol = String(colIdx);
            w.dataset.origIdx = String(idx);
        });
    });
}

function addHandles() {
    document
        .querySelectorAll(".page-column > .widget, .head-widgets > .widget")
        .forEach((w) => {
            if (w.querySelector(":scope > .edit-mode-handles")) return;
            const handles = document.createElement("div");
            handles.className = "edit-mode-handles";
            handles.innerHTML =
                '<button type="button" class="edit-handle edit-handle-drag" title="Drag to reorder" aria-label="Drag">⋮⋮</button>' +
                '<button type="button" class="edit-handle edit-handle-edit" title="Edit" aria-label="Edit">✎</button>' +
                '<button type="button" class="edit-handle edit-handle-delete" title="Delete" aria-label="Delete">✕</button>';
            w.appendChild(handles);
        });
}

function removeHandles() {
    document.querySelectorAll(".edit-mode-handles").forEach((el) => el.remove());
}

function addColumnAddButtons() {
    document.querySelectorAll(".page-column").forEach((col, colIdx) => {
        if (col.querySelector(":scope > .edit-add-widget")) return;
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "edit-add-widget";
        btn.dataset.col = String(colIdx);
        btn.textContent = "+ Add widget";
        col.appendChild(btn);
    });
}

function removeColumnAddButtons() {
    document.querySelectorAll(".edit-add-widget").forEach((b) => b.remove());
}

function inferColumnSize(col) {
    const m = String(col.className || "").match(/page-column-(\S+)/);
    return m ? m[1] : "full";
}

function addColumnHeaders() {
    const cols = document.querySelectorAll(".page-column");
    cols.forEach((col, colIdx) => {
        if (col.querySelector(":scope > .edit-column-header")) return;
        const size = inferColumnSize(col);
        const header = document.createElement("div");
        header.className = "edit-column-header";
        header.dataset.col = String(colIdx);
        header.innerHTML = `
            <span class="edit-column-label">Column ${colIdx + 1}</span>
            <select class="edit-column-size" title="Column size">
                <option value="small" ${size === "small" ? "selected" : ""}>small</option>
                <option value="full" ${size === "full" ? "selected" : ""}>full</option>
            </select>
            <div class="edit-column-spacer"></div>
            <button type="button" class="edit-column-move" data-dir="up" title="Move left" ${colIdx === 0 ? "disabled" : ""}>←</button>
            <button type="button" class="edit-column-move" data-dir="down" title="Move right" ${colIdx === cols.length - 1 ? "disabled" : ""}>→</button>
            <button type="button" class="edit-column-delete" title="Delete column" ${cols.length === 1 ? "disabled" : ""}>✕</button>
        `;
        col.insertBefore(header, col.firstChild);
    });

    // Add a "+ Add column" button in the page-columns container.
    const container = document.querySelector(".page-columns");
    if (container && !container.parentElement.querySelector(":scope > .edit-add-column")) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "edit-add-column";
        btn.textContent = "+ Add column";
        container.parentElement.insertBefore(btn, container.nextSibling);
    }
}

function removeColumnHeaders() {
    document.querySelectorAll(".edit-column-header").forEach((el) => el.remove());
    document.querySelectorAll(".edit-add-column").forEach((el) => el.remove());
}

async function postColumnAction(path, body) {
    const { slug, baseURL } = pageInfo();
    const init = { method: "POST" };
    if (body instanceof URLSearchParams) {
        init.headers = { "Content-Type": "application/x-www-form-urlencoded" };
        init.body = body.toString();
    }
    setStatus("Saving…", "saving");
    const r = await fetch(
        baseURL + "/edit/api/pages/" + encodeURIComponent(slug) + path,
        init,
    );
    if (!r.ok) {
        setStatus("Failed: " + (await r.text()), "error");
        return false;
    }
    location.reload();
    return true;
}

async function addColumn(size) {
    const body = new URLSearchParams();
    body.set("size", size || "full");
    return postColumnAction("/columns", body);
}

async function deleteColumn(col) {
    if (!confirm("Delete this column and everything in it?")) return;
    return postColumnAction(`/columns/${col}/delete`, null);
}

async function moveColumn(col, dir) {
    return postColumnAction(`/columns/${col}/move?dir=${dir}`, null);
}

async function setColumnSize(col, size) {
    const body = new URLSearchParams();
    body.set("size", size);
    return postColumnAction(`/columns/${col}/size`, body);
}

function setStatus(text, kind) {
    let el = document.getElementById("edit-mode-status");
    if (!el) {
        el = document.createElement("div");
        el.id = "edit-mode-status";
        document.body.appendChild(el);
    }
    el.textContent = text;
    el.className = kind || "";
}

async function saveLayout() {
    const { slug } = pageInfo();
    const headWidgets = [];
    document
        .querySelectorAll(".head-widgets > .widget")
        .forEach((w) =>
            headWidgets.push({
                col: parseInt(w.dataset.origCol, 10),
                idx: parseInt(w.dataset.origIdx, 10),
            }),
        );
    const columns = [];
    document.querySelectorAll(".page-column").forEach((col) => {
        const refs = [];
        col.querySelectorAll(":scope > .widget").forEach((w) => {
            refs.push({
                col: parseInt(w.dataset.origCol, 10),
                idx: parseInt(w.dataset.origIdx, 10),
            });
        });
        columns.push(refs);
    });

    setStatus("Saving…", "saving");
    try {
        const r = await fetch(api(`/edit/api/pages/${encodeURIComponent(slug)}/layout`), {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ headWidgets, columns }),
        });
        if (!r.ok) {
            setStatus("Save failed: " + (await r.text()), "error");
            return;
        }
        location.reload();
    } catch (e) {
        setStatus("Save failed: " + e.message, "error");
    }
}

async function deleteWidget(widget) {
    if (!confirm("Delete this widget?")) return;
    const { slug } = pageInfo();
    const col = widget.dataset.origCol;
    const idx = widget.dataset.origIdx;
    setStatus("Deleting…", "saving");
    try {
        const r = await fetch(
            api(`/edit/api/pages/${encodeURIComponent(slug)}/widgets/${col}/${idx}/delete`),
            { method: "POST" },
        );
        if (!r.ok && !r.redirected) {
            setStatus("Delete failed: " + (await r.text()), "error");
            return;
        }
        location.reload();
    } catch (e) {
        setStatus("Delete failed: " + e.message, "error");
    }
}

/* ---------- Schema-driven dialog ---------- */

async function getSchemas() {
    if (state.schemas !== null) return state.schemas;
    try {
        const r = await fetch(api("/edit/api/widget-schemas"));
        state.schemas = r.ok ? (await r.json()) || {} : {};
    } catch {
        state.schemas = {};
    }
    return state.schemas;
}

async function getWidgetFields(col, idx) {
    const { slug } = pageInfo();
    const r = await fetch(
        api(`/edit/api/pages/${encodeURIComponent(slug)}/widgets/${col}/${idx}/fields`),
    );
    if (!r.ok) throw new Error(await r.text());
    return (await r.json()) || {};
}

function renderField(field, value) {
    const required = field.required ? '<span class="edit-required">*</span>' : "";
    const help = field.help ? `<small class="edit-help">${escapeHTML(field.help)}</small>` : "";
    const wrap = (input) => `
        <div class="edit-field" data-field-key="${escapeHTML(field.key)}" data-field-type="${escapeHTML(field.type)}">
            <label class="edit-label">${escapeHTML(field.label)}${required}</label>
            ${help}
            ${input}
        </div>`;

    const lookupAttr = field.lookup ? ` data-lookup="${escapeHTML(field.lookup)}"` : "";
    const validatorAttr = field.validator ? ` data-validator="${escapeHTML(field.validator)}"` : "";

    switch (field.type) {
        case "string":
            if (field.lookup) {
                return wrap(`
                    <div class="edit-autocomplete">
                        <input type="text" class="edit-input"${lookupAttr}${validatorAttr} value="${escapeHTML(value)}" ${field.required ? "required" : ""} autocomplete="off">
                        <div class="edit-autocomplete-list" hidden></div>
                        <div class="edit-validate-state"></div>
                    </div>`);
            }
            return wrap(
                `<input type="text" class="edit-input"${validatorAttr} value="${escapeHTML(value)}" ${field.required ? "required" : ""}>` +
                (field.validator ? '<div class="edit-validate-state"></div>' : ""),
            );
        case "multiline":
            return wrap(
                `<textarea class="edit-input" rows="6" ${field.required ? "required" : ""}>${escapeHTML(value)}</textarea>`,
            );
        case "number":
            return wrap(
                `<input type="number" class="edit-input" value="${value ?? ""}" ${field.required ? "required" : ""}>`,
            );
        case "boolean":
            return `
                <div class="edit-field" data-field-key="${escapeHTML(field.key)}" data-field-type="boolean">
                    <label class="edit-checkbox"><input type="checkbox" ${value ? "checked" : ""}><span>${escapeHTML(field.label)}</span></label>
                    ${help}
                </div>`;
        case "select": {
            const opts = (field.options || [])
                .map(
                    (o) =>
                        `<option value="${escapeHTML(o)}" ${value === o ? "selected" : ""}>${escapeHTML(o)}</option>`,
                )
                .join("");
            return wrap(
                `<select class="edit-input" ${field.required ? "required" : ""}><option value=""></option>${opts}</select>`,
            );
        }
        case "list-strings":
            return wrap(renderListStrings(value));
        case "list-objects":
            return wrap(renderListObjects(field.items, value));
        case "list-of-widgets":
            return wrap(renderListOfWidgets(value));
        default:
            return wrap(`<em>Unsupported type: ${escapeHTML(field.type)}</em>`);
    }
}

// Widget types allowed inside a group/split-column. Nested groups and
// split-columns are rejected by the backend, so we hide them in the picker.
function nestedWidgetTypes() {
    const all = Object.keys(state.schemas || {});
    return all
        .filter((t) => t !== "group" && t !== "split-column")
        .sort();
}

function renderListOfWidgets(values) {
    values = Array.isArray(values) ? values : [];
    const items = values
        .map((v) => renderNestedWidget(v?.type || "clock", v || {}))
        .join("");
    return `
        <div class="edit-list edit-list-widgets">
            <div class="edit-list-items">${items}</div>
            <button type="button" class="edit-list-add" data-add-widget="1">+ Add widget</button>
        </div>`;
}

function renderNestedWidget(widgetType, values) {
    const schema = state.schemas?.[widgetType] || [];
    const typeOptions = nestedWidgetTypes()
        .map(
            (t) =>
                `<option value="${escapeHTML(t)}" ${t === widgetType ? "selected" : ""}>${escapeHTML(t)}</option>`,
        )
        .join("");
    return `
        <div class="edit-list-item edit-nested-widget" data-type="${escapeHTML(widgetType)}">
            <div class="edit-nested-header">
                <select class="edit-nested-type" title="Widget type">${typeOptions}</select>
                <button type="button" class="edit-list-remove">Remove</button>
            </div>
            <div class="edit-nested-body">${renderForm(schema, values)}</div>
        </div>`;
}

function renderListStrings(values) {
    values = Array.isArray(values) ? values : [];
    const items = values
        .map(
            (v) => `
        <div class="edit-list-item">
            <input type="text" class="edit-input" value="${escapeHTML(v)}">
            <button type="button" class="edit-list-remove" aria-label="Remove">×</button>
        </div>`,
        )
        .join("");
    return `
        <div class="edit-list edit-list-strings">
            <div class="edit-list-items">${items}</div>
            <button type="button" class="edit-list-add">+ Add</button>
        </div>`;
}

function renderListObjects(itemsSchema, values) {
    values = Array.isArray(values) ? values : [];
    const items = values.map((v) => renderListObjectItem(itemsSchema, v)).join("");
    const schemaJSON = escapeHTML(JSON.stringify(itemsSchema));
    return `
        <div class="edit-list edit-list-objects" data-items-schema="${schemaJSON}">
            <div class="edit-list-items">${items}</div>
            <button type="button" class="edit-list-add">+ Add</button>
        </div>`;
}

function renderListObjectItem(itemsSchema, value) {
    const fields = itemsSchema.map((f) => renderField(f, value?.[f.key])).join("");
    return `
        <div class="edit-list-item edit-list-object">
            ${fields}
            <button type="button" class="edit-list-remove">Remove</button>
        </div>`;
}

function renderForm(schema, values) {
    return schema.map((f) => renderField(f, values?.[f.key])).join("");
}

/* ---------- Collect values ---------- */

function collectValues(container, schema) {
    const result = {};
    for (const f of schema) {
        const fieldEl = container.querySelector(
            `:scope > .edit-field[data-field-key="${cssEscape(f.key)}"]`,
        );
        result[f.key] = fieldEl ? collectFieldValue(fieldEl, f) : null;
    }
    return result;
}

function collectFieldValue(fieldEl, field) {
    switch (field.type) {
        case "string":
        case "multiline": {
            // Inputs may be wrapped in .edit-autocomplete when the schema
            // declares a lookup, so we can't rely on a direct-child selector.
            const el = fieldEl.querySelector("input, textarea");
            const v = (el?.value ?? "").trim();
            return v === "" ? null : v;
        }
        case "number": {
            const el = fieldEl.querySelector("input");
            const v = (el?.value ?? "").trim();
            if (v === "") return null;
            const n = Number(v);
            return Number.isNaN(n) ? null : n;
        }
        case "boolean": {
            const el = fieldEl.querySelector(":scope > .edit-checkbox > input[type=checkbox]");
            return !!el?.checked;
        }
        case "select": {
            const el = fieldEl.querySelector(":scope > select");
            return el?.value || null;
        }
        case "list-strings": {
            const inputs = fieldEl.querySelectorAll(
                ":scope > .edit-list > .edit-list-items > .edit-list-item > input",
            );
            const arr = [...inputs].map((i) => i.value.trim()).filter(Boolean);
            return arr.length ? arr : null;
        }
        case "list-objects": {
            const items = fieldEl.querySelectorAll(
                ":scope > .edit-list > .edit-list-items > .edit-list-object",
            );
            const arr = [...items].map((item) => collectValues(item, field.items));
            const filtered = arr.filter((o) =>
                Object.values(o).some(
                    (v) => v !== null && v !== "" && !(Array.isArray(v) && v.length === 0),
                ),
            );
            return filtered.length ? filtered : null;
        }
        case "list-of-widgets": {
            const items = fieldEl.querySelectorAll(
                ":scope > .edit-list > .edit-list-items > .edit-nested-widget",
            );
            const arr = [...items].map((item) => {
                const type = item.dataset.type;
                const body = item.querySelector(":scope > .edit-nested-body");
                const schema = state.schemas?.[type] || [];
                const values = collectValues(body, schema);
                // Drop null/empty fields; emit type plus whatever was filled in.
                const out = { type };
                for (const [k, v] of Object.entries(values)) {
                    if (v !== null && !(Array.isArray(v) && v.length === 0)) {
                        out[k] = v;
                    }
                }
                return out;
            });
            return arr.length ? arr : null;
        }
    }
    return null;
}

/* ---------- Autocomplete + validation ---------- */

const lookupCache = new Map(); // key: kind + ":" + query -> suggestions

async function fetchLookup(kind, query) {
    const key = kind + ":" + query;
    if (lookupCache.has(key)) return lookupCache.get(key);
    try {
        const r = await fetch(api(`/edit/api/lookup/${encodeURIComponent(kind)}`), {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ query }),
        });
        if (!r.ok) return [];
        const data = await r.json();
        const list = data.suggestions || [];
        lookupCache.set(key, list);
        return list;
    } catch {
        return [];
    }
}

async function fetchValidation(kind, value) {
    try {
        const r = await fetch(api(`/edit/api/validate/${encodeURIComponent(kind)}`), {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ value }),
        });
        if (!r.ok) return { valid: false, error: await r.text() };
        return await r.json();
    } catch (e) {
        return { valid: false, error: e.message };
    }
}

function debounce(fn, ms) {
    let t = null;
    return (...args) => {
        clearTimeout(t);
        t = setTimeout(() => fn(...args), ms);
    };
}

function renderSuggestions(listEl, suggestions) {
    if (!suggestions || suggestions.length === 0) {
        listEl.hidden = true;
        listEl.innerHTML = "";
        return;
    }
    listEl.hidden = false;
    listEl.innerHTML = suggestions
        .map(
            (s, i) => `
        <button type="button" class="edit-autocomplete-item" data-index="${i}">
            <div class="edit-autocomplete-display">${escapeHTML(s.display || s.value)}</div>
            ${s.hint ? `<div class="edit-autocomplete-hint">${escapeHTML(s.hint)}</div>` : ""}
        </button>`,
        )
        .join("");
    // Stash data on the list element for the click handler.
    listEl._suggestions = suggestions;
}

function attachLookup(input) {
    if (input._lookupAttached) return;
    input._lookupAttached = true;

    const kind = input.dataset.lookup;
    const wrapper = input.closest(".edit-autocomplete");
    const listEl = wrapper?.querySelector(":scope > .edit-autocomplete-list");
    if (!wrapper || !listEl) return;

    const updateSuggestions = debounce(async () => {
        const q = input.value.trim();
        if (q.length < 2) {
            listEl.hidden = true;
            return;
        }
        const suggestions = await fetchLookup(kind, q);
        if (input.value.trim() !== q) return; // user kept typing
        renderSuggestions(listEl, suggestions);
    }, 300);

    input.addEventListener("input", updateSuggestions);
    input.addEventListener("focus", () => {
        if (listEl.children.length > 0) listEl.hidden = false;
    });
    input.addEventListener("blur", () => {
        // Delay so a click on a suggestion can register first.
        setTimeout(() => (listEl.hidden = true), 150);
    });

    listEl.addEventListener("mousedown", (e) => {
        const btn = e.target.closest(".edit-autocomplete-item");
        if (!btn) return;
        e.preventDefault(); // keep focus on the input
        const idx = parseInt(btn.dataset.index, 10);
        const s = listEl._suggestions?.[idx];
        if (!s) return;
        input.value = s.value;
        // Auto-fill sibling fields if the suggestion provides extras.
        if (s.extra) {
            const itemScope =
                input.closest(".edit-list-object") || input.closest(".edit-dialog-body");
            for (const [k, v] of Object.entries(s.extra)) {
                const sibling = itemScope?.querySelector(
                    `:scope > .edit-field[data-field-key="${cssEscape(k)}"] input, :scope > .edit-field[data-field-key="${cssEscape(k)}"] textarea`,
                );
                if (sibling && !sibling.value.trim()) {
                    sibling.value = v;
                }
            }
        }
        listEl.hidden = true;
        listEl.innerHTML = "";
        clearValidationState(input);
    });
}

function setValidationState(input, kind, message) {
    const stateEl =
        input
            .closest(".edit-autocomplete, .edit-field")
            ?.querySelector(":scope > .edit-validate-state, :scope .edit-validate-state");
    if (!stateEl) return;
    stateEl.className = "edit-validate-state edit-validate-" + kind;
    stateEl.textContent = message || "";
    input.classList.toggle("edit-input-invalid", kind === "error");
}

function clearValidationState(input) {
    const stateEl =
        input
            .closest(".edit-autocomplete, .edit-field")
            ?.querySelector(":scope > .edit-validate-state, :scope .edit-validate-state");
    if (stateEl) {
        stateEl.className = "edit-validate-state";
        stateEl.textContent = "";
    }
    input.classList.remove("edit-input-invalid");
}

function isEmpty(v) {
    if (v === null || v === undefined) return true;
    if (typeof v === "string") return v.trim() === "";
    if (Array.isArray(v)) return v.length === 0;
    return false;
}

// Walk the form recursively and return errors for any required field that's
// empty. This duplicates HTML5's `required`, but the dialog's Save button
// isn't an actual form-submit, so we have to enforce it ourselves.
function checkRequired(container, schema) {
    const errors = [];
    for (const f of schema) {
        const fieldEl = container.querySelector(
            `:scope > .edit-field[data-field-key="${cssEscape(f.key)}"]`,
        );
        if (!fieldEl) continue;
        const value = collectFieldValue(fieldEl, f);
        if (f.required && isEmpty(value)) {
            const input = fieldEl.querySelector("input, textarea, select");
            errors.push({ input, fieldEl, message: `${f.label} is required.` });
        }
        if (f.type === "list-objects") {
            const items = fieldEl.querySelectorAll(
                ":scope > .edit-list > .edit-list-items > .edit-list-object",
            );
            items.forEach((item) => {
                const itemValues = collectValues(item, f.items);
                // Skip all-empty rows; they'll be filtered out on save.
                if (Object.values(itemValues).every(isEmpty)) return;
                errors.push(...checkRequired(item, f.items));
            });
        }
    }
    return errors;
}

async function preSubmitValidate(dialogBody, schema) {
    const errors = [];

    // First pass: required-field check.
    const requiredErrors = checkRequired(dialogBody, schema);
    for (const e of requiredErrors) {
        if (e.input) setValidationState(e.input, "error", e.message);
        errors.push(e);
    }
    if (errors.length > 0) return errors;

    // Second pass: network validators (only for non-empty inputs).
    const inputs = dialogBody.querySelectorAll("input[data-validator]");
    for (const input of inputs) {
        const value = input.value.trim();
        if (value === "") continue;
        const kind = input.dataset.validator;
        setValidationState(input, "checking", "Checking…");
        const r = await fetchValidation(kind, value);
        if (r.valid) {
            setValidationState(input, "ok", r.hint ? "✓ " + r.hint : "✓");
        } else {
            setValidationState(input, "error", r.error || "Invalid value");
            errors.push({ input, message: r.error });
        }
    }
    return errors;
}

/* ---------- Dialog ---------- */

function openDialog({ title, schema, values, submitLabel, onSubmit }) {
    const overlay = document.createElement("div");
    overlay.className = "edit-dialog-overlay";
    overlay.innerHTML = `
        <div class="edit-dialog" role="dialog" aria-modal="true">
            <div class="edit-dialog-header">
                <h2>${escapeHTML(title)}</h2>
                <button type="button" class="edit-dialog-close" aria-label="Close">×</button>
            </div>
            <div class="edit-dialog-body">${renderForm(schema, values)}</div>
            <div class="edit-dialog-error" hidden></div>
            <div class="edit-dialog-actions">
                <button type="button" class="edit-dialog-cancel">Cancel</button>
                <button type="button" class="edit-dialog-save">${escapeHTML(submitLabel || "Save")}</button>
            </div>
        </div>`;

    const close = () => overlay.remove();

    overlay.addEventListener("click", (e) => {
        if (e.target === overlay) {
            close();
            return;
        }
        if (e.target.closest(".edit-dialog-close, .edit-dialog-cancel")) {
            close();
            return;
        }
        const removeBtn = e.target.closest(".edit-list-remove");
        if (removeBtn) {
            removeBtn.closest(".edit-list-item").remove();
            return;
        }
        const addBtn = e.target.closest(".edit-list-add");
        if (addBtn) {
            const list = addBtn.closest(".edit-list");
            const items = list.querySelector(":scope > .edit-list-items");
            if (list.classList.contains("edit-list-strings")) {
                const div = document.createElement("div");
                div.className = "edit-list-item";
                div.innerHTML =
                    '<input type="text" class="edit-input"><button type="button" class="edit-list-remove" aria-label="Remove">×</button>';
                items.appendChild(div);
                div.querySelector("input").focus();
            } else if (list.classList.contains("edit-list-objects")) {
                const itemsSchema = JSON.parse(list.dataset.itemsSchema);
                const tmp = document.createElement("div");
                tmp.innerHTML = renderListObjectItem(itemsSchema, {});
                items.appendChild(tmp.firstElementChild);
                items.lastElementChild.querySelector("input, textarea, select")?.focus();
            } else if (list.classList.contains("edit-list-widgets")) {
                const defaultType = nestedWidgetTypes()[0] || "clock";
                const tmp = document.createElement("div");
                tmp.innerHTML = renderNestedWidget(defaultType, {});
                items.appendChild(tmp.firstElementChild);
                items.lastElementChild
                    .querySelector(".edit-nested-body input, .edit-nested-body textarea, .edit-nested-body select")
                    ?.focus();
            }
        }
    });

    // Type-switching for nested widgets: re-render the item's body with the
    // schema for the newly chosen type.
    overlay.addEventListener("change", (e) => {
        const sel = e.target.closest(".edit-nested-type");
        if (!sel) return;
        const item = sel.closest(".edit-nested-widget");
        const newType = sel.value;
        item.dataset.type = newType;
        const body = item.querySelector(":scope > .edit-nested-body");
        const schema = state.schemas?.[newType] || [];
        body.innerHTML = renderForm(schema, {});
    });

    overlay.addEventListener("keydown", (e) => {
        if (e.key === "Escape") close();
    });

    overlay.querySelector(".edit-dialog-save").addEventListener("click", async () => {
        const errEl = overlay.querySelector(".edit-dialog-error");
        const saveBtn = overlay.querySelector(".edit-dialog-save");
        errEl.hidden = true;
        saveBtn.disabled = true;
        try {
            const validationErrors = await preSubmitValidate(
                overlay.querySelector(".edit-dialog-body"),
                schema,
            );
            if (validationErrors.length > 0) {
                errEl.textContent = "Some fields didn't validate — see details next to each one.";
                errEl.hidden = false;
                validationErrors[0].input?.focus();
                return;
            }
            const newValues = collectValues(overlay.querySelector(".edit-dialog-body"), schema);
            await onSubmit(newValues);
        } catch (e) {
            errEl.textContent = e.message || String(e);
            errEl.hidden = false;
            errEl.scrollIntoView({ behavior: "smooth", block: "nearest" });
        } finally {
            saveBtn.disabled = false;
        }
    });

    document.body.appendChild(overlay);
    overlay.querySelector("input, textarea, select")?.focus();
    // Wire up autocomplete on any pre-rendered or future inputs with a lookup.
    overlay.querySelectorAll("input[data-lookup]").forEach(attachLookup);
    new MutationObserver((records) => {
        for (const r of records) {
            r.addedNodes?.forEach((n) => {
                if (n.nodeType !== 1) return;
                n.querySelectorAll?.("input[data-lookup]").forEach(attachLookup);
                if (n.matches?.("input[data-lookup]")) attachLookup(n);
            });
        }
    }).observe(overlay, { childList: true, subtree: true });
    // Clear validation hints when user types in a validator-flagged input.
    overlay.addEventListener("input", (e) => {
        if (e.target.matches?.("input[data-validator]")) {
            clearValidationState(e.target);
        }
    });
}

async function openEditDialog(col, idx, widgetType) {
    const { slug, baseURL } = pageInfo();
    const schemas = await getSchemas();
    const schema = schemas[widgetType];
    if (!schema) {
        // No schema — fall back to YAML editor for this widget type.
        location.href = `${baseURL}/edit/pages/${encodeURIComponent(slug)}/widgets/${col}/${idx}`;
        return;
    }
    let values = {};
    try {
        values = await getWidgetFields(col, idx);
    } catch (e) {
        alert("Couldn't load widget fields: " + e.message);
        return;
    }

    openDialog({
        title: `Edit ${widgetType}`,
        schema,
        values,
        submitLabel: "Save",
        async onSubmit(newValues) {
            const r = await fetch(
                api(
                    `/edit/api/pages/${encodeURIComponent(slug)}/widgets/${col}/${idx}/fields`,
                ),
                {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify(newValues),
                },
            );
            if (!r.ok) throw new Error(await r.text());
            location.reload();
        },
    });
}

async function openCreateDialog(col, widgetType) {
    const { slug, baseURL } = pageInfo();
    const schemas = await getSchemas();
    const schema = schemas[widgetType];
    if (!schema) {
        location.href = `${baseURL}/edit/pages/${encodeURIComponent(slug)}/widgets/${col}/new?type=${encodeURIComponent(widgetType)}`;
        return;
    }

    openDialog({
        title: `Add ${widgetType}`,
        schema,
        values: {},
        submitLabel: "Create",
        async onSubmit(newValues) {
            const r = await fetch(
                api(`/edit/api/pages/${encodeURIComponent(slug)}/widgets/${col}/create`),
                {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ type: widgetType, fields: newValues }),
                },
            );
            if (!r.ok) throw new Error(await r.text());
            location.reload();
        },
    });
}

async function showAddPicker(col) {
    const { slug, baseURL } = pageInfo();
    const schemas = await getSchemas();
    const types = Object.keys(schemas).sort();

    const overlay = document.createElement("div");
    overlay.className = "edit-dialog-overlay";
    overlay.innerHTML = `
        <div class="edit-dialog edit-dialog-narrow" role="dialog" aria-modal="true">
            <div class="edit-dialog-header">
                <h2>Add widget</h2>
                <button type="button" class="edit-dialog-close" aria-label="Close">×</button>
            </div>
            <div class="edit-dialog-body">
                <div class="edit-type-grid">
                    ${types.map((t) => `<button type="button" class="edit-type-option" data-type="${escapeHTML(t)}">${escapeHTML(t)}</button>`).join("")}
                </div>
                <p class="edit-help" style="margin-top:1rem;">
                    Need a widget type that isn't listed? Use the
                    <a href="${baseURL}/edit/pages/${encodeURIComponent(slug)}" target="_blank">advanced editor</a>.
                </p>
            </div>
        </div>`;
    const close = () => overlay.remove();
    overlay.addEventListener("click", (e) => {
        if (e.target === overlay || e.target.closest(".edit-dialog-close")) {
            close();
            return;
        }
        const btn = e.target.closest(".edit-type-option");
        if (btn) {
            close();
            openCreateDialog(col, btn.dataset.type);
        }
    });
    overlay.addEventListener("keydown", (e) => {
        if (e.key === "Escape") close();
    });
    document.body.appendChild(overlay);
}

/* ---------- Mode toggle ---------- */

function enterEditMode() {
    if (state.active) return;
    if (typeof Sortable === "undefined") {
        console.error("edit-mode: Sortable library not loaded");
        return;
    }
    state.active = true;
    document.body.dataset.editMode = "true";
    document.querySelectorAll("#edit-mode-toggle").forEach((b) => b.classList.add("active"));
    indexWidgets();
    addHandles();
    addColumnHeaders();
    addColumnAddButtons();
    setStatus("Edit mode — drag widgets to rearrange");

    document.querySelectorAll(".page-column").forEach((col) => {
        state.sortables.push(
            Sortable.create(col, {
                group: "glance-widgets",
                handle: ".edit-handle-drag",
                draggable: ".widget",
                animation: 150,
                ghostClass: "sortable-ghost",
                dragClass: "sortable-drag",
                onEnd: () => saveLayout(),
            }),
        );
    });
    // Head widgets get their own group so they can't be dragged into columns
    // (they live in a separate yaml sequence and render differently).
    document.querySelectorAll(".head-widgets").forEach((hw) => {
        state.sortables.push(
            Sortable.create(hw, {
                group: "glance-head-widgets",
                handle: ".edit-handle-drag",
                draggable: ".widget",
                animation: 150,
                ghostClass: "sortable-ghost",
                dragClass: "sortable-drag",
                onEnd: () => saveLayout(),
            }),
        );
    });

    localStorage.setItem(STORAGE_KEY, "1");
    // Pre-warm schema cache so first dialog open is instant.
    getSchemas();
}

function exitEditMode() {
    if (!state.active) return;
    state.active = false;
    document.body.removeAttribute("data-edit-mode");
    document.querySelectorAll("#edit-mode-toggle").forEach((b) => b.classList.remove("active"));
    state.sortables.forEach((s) => s.destroy());
    state.sortables = [];
    removeHandles();
    removeColumnHeaders();
    removeColumnAddButtons();
    document.getElementById("edit-mode-status")?.remove();
    localStorage.removeItem(STORAGE_KEY);
}

function toggleEditMode() {
    state.active ? exitEditMode() : enterEditMode();
}

function waitForContent(callback) {
    const target = document.getElementById("page-content");
    if (!target) return;
    if (target.children.length > 0) {
        callback();
        return;
    }
    const obs = new MutationObserver(() => {
        if (target.children.length > 0) {
            obs.disconnect();
            callback();
        }
    });
    obs.observe(target, { childList: true });
}

/* ---------- Click routing ---------- */

document.addEventListener("click", (e) => {
    const toggle = e.target.closest("#edit-mode-toggle");
    if (toggle) {
        e.preventDefault();
        toggleEditMode();
        return;
    }
    const editBtn = e.target.closest(".edit-handle-edit");
    if (editBtn) {
        e.preventDefault();
        const widget = editBtn.closest(".widget");
        if (widget) {
            openEditDialog(
                parseInt(widget.dataset.origCol, 10),
                parseInt(widget.dataset.origIdx, 10),
                widgetTypeOf(widget),
            );
        }
        return;
    }
    const delBtn = e.target.closest(".edit-handle-delete");
    if (delBtn) {
        e.preventDefault();
        const widget = delBtn.closest(".widget");
        if (widget) deleteWidget(widget);
        return;
    }
    const addBtn = e.target.closest(".edit-add-widget");
    if (addBtn) {
        e.preventDefault();
        showAddPicker(parseInt(addBtn.dataset.col, 10));
        return;
    }
    const addColBtn = e.target.closest(".edit-add-column");
    if (addColBtn) {
        e.preventDefault();
        const size = prompt("Column size: 'small' or 'full'", "full");
        if (size && (size === "small" || size === "full")) addColumn(size);
        return;
    }
    const moveColBtn = e.target.closest(".edit-column-move");
    if (moveColBtn) {
        e.preventDefault();
        const header = moveColBtn.closest(".edit-column-header");
        moveColumn(parseInt(header.dataset.col, 10), moveColBtn.dataset.dir);
        return;
    }
    const delColBtn = e.target.closest(".edit-column-delete");
    if (delColBtn) {
        e.preventDefault();
        const header = delColBtn.closest(".edit-column-header");
        deleteColumn(parseInt(header.dataset.col, 10));
        return;
    }
});

document.addEventListener("change", (e) => {
    const sizeSel = e.target.closest(".edit-column-size");
    if (sizeSel) {
        const header = sizeSel.closest(".edit-column-header");
        setColumnSize(parseInt(header.dataset.col, 10), sizeSel.value);
    }
});

waitForContent(() => {
    if (localStorage.getItem(STORAGE_KEY) === "1") {
        enterEditMode();
    }
});
