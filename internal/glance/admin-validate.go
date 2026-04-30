package glance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

// admin-validate.go — server-side validation and lookup helpers used by the
// edit-mode dialog. Each kind has both:
//   - validate(value): is this exact value usable?
//   - lookup(query): suggestions for typing-as-you-go
// Where one of those isn't applicable (e.g. there's no "search" for an
// arbitrary RSS URL), only the relevant function is implemented.

type validationSuggestion struct {
	Value   string            `json:"value"`           // the string to put into the input
	Display string            `json:"display"`         // human-readable label for the dropdown
	Hint    string            `json:"hint,omitempty"`  // small caption (e.g. "AAPL — Apple Inc.")
	Extra   map[string]string `json:"extra,omitempty"` // sibling field values to auto-fill (e.g. {"name": "Apple Inc."})
}

type validationResult struct {
	Valid       bool                   `json:"valid"`
	Error       string                 `json:"error,omitempty"`
	Hint        string                 `json:"hint,omitempty"` // shown next to a successful field
	Suggestions []validationSuggestion `json:"suggestions,omitempty"`
}

var validateClient = &http.Client{Timeout: 8 * time.Second}

func ctxWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func httpGetJSON(rawURL string, into interface{}) error {
	ctx, cancel := ctxWithTimeout(8 * time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	// Yahoo's undocumented endpoints reject the default Go UA. Use a browser-y
	// string so search/quote actually return data.
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json,text/plain,*/*")
	resp, err := validateClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(into)
}

// ---------- weather-location (Open-Meteo geocoding) ----------

type openMeteoGeo struct {
	Results []struct {
		Name        string  `json:"name"`
		Country     string  `json:"country"`
		Admin1      string  `json:"admin1"`
		CountryCode string  `json:"country_code"`
		Latitude    float64 `json:"latitude"`
		Longitude   float64 `json:"longitude"`
	} `json:"results"`
}

func geocodeWeatherLocation(query string) ([]validationSuggestion, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	endpoint := "https://geocoding-api.open-meteo.com/v1/search?count=8&format=json&name=" + url.QueryEscape(q)
	var data openMeteoGeo
	if err := httpGetJSON(endpoint, &data); err != nil {
		return nil, err
	}
	out := make([]validationSuggestion, 0, len(data.Results))
	for _, r := range data.Results {
		// Glance's weather widget accepts strings like "London, GB". We use that
		// canonical form as the suggestion value so saving works without changes.
		v := r.Name
		if r.CountryCode != "" {
			v = v + ", " + r.CountryCode
		}
		display := r.Name
		if r.Admin1 != "" {
			display += ", " + r.Admin1
		}
		if r.Country != "" {
			display += ", " + r.Country
		}
		out = append(out, validationSuggestion{
			Value:   v,
			Display: display,
			Hint:    fmt.Sprintf("%.2f, %.2f", r.Latitude, r.Longitude),
		})
	}
	return out, nil
}

func validateWeatherLocation(value string) validationResult {
	v := strings.TrimSpace(value)
	if v == "" {
		return validationResult{Valid: false, Error: "Location is required."}
	}
	// Try to verify against Open-Meteo, but never block the save on failure
	// — the user might know better than us, and the weather widget will
	// surface a clear error at update time if it really can't geocode.
	searchQuery := v
	if comma := strings.IndexByte(v, ','); comma > 0 {
		searchQuery = strings.TrimSpace(v[:comma])
	}
	suggestions, err := geocodeWeatherLocation(searchQuery)
	if err != nil || len(suggestions) == 0 {
		return validationResult{Valid: true, Hint: "couldn't verify — will be tested when the widget updates"}
	}
	return validationResult{Valid: true, Hint: suggestions[0].Display}
}

// ---------- market-symbol (Yahoo Finance) ----------

type yahooSearchResp struct {
	Quotes []struct {
		Symbol      string `json:"symbol"`
		ShortName   string `json:"shortname"`
		LongName    string `json:"longname"`
		QuoteType   string `json:"quoteType"`
		Exchange    string `json:"exchDisp"`
	} `json:"quotes"`
}

type yahooChartResp struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				LongName           string  `json:"longName"`
				ShortName          string  `json:"shortName"`
				ExchangeName       string  `json:"exchangeName"`
				FullExchangeName   string  `json:"fullExchangeName"`
				Currency           string  `json:"currency"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
			} `json:"meta"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func lookupMarketSymbol(query string) ([]validationSuggestion, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	// Try query1 first; fall back to query2 since one or the other is usually up.
	var data yahooSearchResp
	q1 := "https://query1.finance.yahoo.com/v1/finance/search?quotesCount=8&newsCount=0&q=" + url.QueryEscape(q)
	if err := httpGetJSON(q1, &data); err != nil || len(data.Quotes) == 0 {
		q2 := "https://query2.finance.yahoo.com/v1/finance/search?quotesCount=8&newsCount=0&q=" + url.QueryEscape(q)
		if err2 := httpGetJSON(q2, &data); err2 != nil {
			return nil, err2
		}
	}
	out := make([]validationSuggestion, 0, len(data.Quotes))
	for _, qr := range data.Quotes {
		name := qr.LongName
		if name == "" {
			name = qr.ShortName
		}
		if qr.Symbol == "" {
			continue
		}
		display := qr.Symbol
		if name != "" {
			display = qr.Symbol + " — " + name
		}
		hint := qr.QuoteType
		if qr.Exchange != "" {
			hint = strings.TrimSpace(strings.Join([]string{qr.QuoteType, qr.Exchange}, " · "))
		}
		extra := map[string]string{}
		if name != "" {
			extra["name"] = name
		}
		out = append(out, validationSuggestion{
			Value:   qr.Symbol,
			Display: display,
			Hint:    hint,
			Extra:   extra,
		})
	}
	return out, nil
}

func validateMarketSymbol(value string) validationResult {
	sym := strings.ToUpper(strings.TrimSpace(value))
	if sym == "" {
		return validationResult{Valid: false, Error: "Symbol is required."}
	}
	// v8 chart endpoint is the most reliable Yahoo entry point — it serves
	// public data and tends to ignore the auth cookies the v7 quote API
	// started requiring. We just need to confirm the symbol resolves to a
	// real instrument; we don't care about price.
	endpoint := "https://query1.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(sym) + "?range=1d&interval=1d"
	var data yahooChartResp
	if err := httpGetJSON(endpoint, &data); err != nil {
		return validationResult{Valid: true, Hint: "couldn't verify (Yahoo unreachable)"}
	}
	if data.Chart.Error != nil && data.Chart.Error.Code != "" {
		return validationResult{Valid: false, Error: "Yahoo: " + data.Chart.Error.Description}
	}
	if len(data.Chart.Result) == 0 {
		return validationResult{Valid: false, Error: "No data returned for " + sym + "."}
	}
	m := data.Chart.Result[0].Meta
	name := m.LongName
	if name == "" {
		name = m.ShortName
	}
	hint := name
	if m.FullExchangeName != "" && name != "" {
		hint = name + " · " + m.FullExchangeName
	}
	return validationResult{Valid: true, Hint: hint}
}

// ---------- rss-feed (gofeed) ----------

func validateRSSFeed(value string) validationResult {
	u := strings.TrimSpace(value)
	if u == "" {
		return validationResult{Valid: false, Error: "URL is required."}
	}
	if _, err := url.ParseRequestURI(u); err != nil {
		return validationResult{Valid: false, Error: "Doesn't look like a URL."}
	}
	ctx, cancel := ctxWithTimeout(8 * time.Second)
	defer cancel()
	parser := gofeed.NewParser()
	parser.Client = validateClient
	feed, err := parser.ParseURLWithContext(u, ctx)
	if err != nil {
		return validationResult{Valid: false, Error: "Feed parse failed: " + err.Error()}
	}
	hint := feed.Title
	if feed.Items != nil {
		hint = fmt.Sprintf("%s · %d items", feed.Title, len(feed.Items))
	}
	return validationResult{Valid: true, Hint: hint}
}

// ---------- HTTP handlers ----------

func (a *application) handleAdminValidate(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	kind := r.PathValue("kind")
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var result validationResult
	switch kind {
	case "weather-location":
		result = validateWeatherLocation(body.Value)
	case "market-symbol":
		result = validateMarketSymbol(body.Value)
	case "rss-feed":
		result = validateRSSFeed(body.Value)
	default:
		http.Error(w, "unknown validator: "+kind, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (a *application) handleAdminLookup(w http.ResponseWriter, r *http.Request) {
	if !a.adminAccessAllowed(w, r) {
		return
	}
	kind := r.PathValue("kind")
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var (
		suggestions []validationSuggestion
		err         error
	)
	switch kind {
	case "weather-location":
		suggestions, err = geocodeWeatherLocation(body.Query)
	case "market-symbol":
		suggestions, err = lookupMarketSymbol(body.Query)
	default:
		http.Error(w, "unknown lookup: "+kind, http.StatusNotFound)
		return
	}
	if err != nil {
		// Lookup failures shouldn't be hard errors — return empty suggestions
		// so the UI just shows nothing rather than alarming the user.
		suggestions = nil
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Suggestions []validationSuggestion `json:"suggestions"`
	}{Suggestions: suggestions})
}
