package gsmarena

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	quickSearchPattern  = regexp.MustCompile(`quicksearch-[0-9]+\.jpg`)
	historyIDPattern    = regexp.MustCompile(`(?i)HISTORY_ITEM_ID\s*=\s*["']([0-9]+)`)
	historyURLPattern   = regexp.MustCompile(`(?i)HISTORY_ITEM_URL\s*=\s*["']([^"']+)`)
	historyImagePattern = regexp.MustCompile(`(?i)HISTORY_ITEM_IMAGE\s*=\s*["']([^"']+)`)
	phoneIDSuffix       = regexp.MustCompile(`-([0-9]+)$`)
)

func parseQuickSearch(contents []byte) (quickSearchIndex, error) {
	contents = bytes.TrimSpace(contents)
	if len(contents) > 0 && contents[0] == '<' {
		document, err := goquery.NewDocumentFromReader(bytes.NewReader(contents))
		if err != nil {
			return quickSearchIndex{}, fmt.Errorf("parse quicksearch wrapper: %w", err)
		}
		contents = []byte(strings.TrimSpace(document.Find("pre").First().Text()))
	}
	var payload []json.RawMessage
	if err := json.Unmarshal(contents, &payload); err != nil {
		return quickSearchIndex{}, fmt.Errorf("decode GSMArena quicksearch index: %w", err)
	}
	if len(payload) != 2 {
		return quickSearchIndex{}, fmt.Errorf("decode GSMArena quicksearch index: expected two elements, got %d", len(payload))
	}

	var rawBrands map[string]string
	if err := json.Unmarshal(payload[0], &rawBrands); err != nil {
		return quickSearchIndex{}, fmt.Errorf("decode GSMArena brands: %w", err)
	}
	var rawRecords []json.RawMessage
	if err := json.Unmarshal(payload[1], &rawRecords); err != nil {
		return quickSearchIndex{}, fmt.Errorf("decode GSMArena phones: %w", err)
	}
	brands := make(map[int64]string, len(rawBrands))
	for rawID, name := range rawBrands {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			return quickSearchIndex{}, fmt.Errorf("decode GSMArena brand ID %q: %w", rawID, err)
		}
		if name = strings.TrimSpace(name); name != "" {
			brands[id] = name
		}
	}
	records := make([]quickSearchRecord, 0, len(rawRecords))
	for index, raw := range rawRecords {
		record, err := parseQuickSearchRecord(raw)
		if err != nil {
			return quickSearchIndex{}, fmt.Errorf("decode GSMArena phone %d: %w", index, err)
		}
		if record.ID != 0 && record.Model != "" {
			records = append(records, record)
		}
	}
	return quickSearchIndex{Brands: brands, Records: records}, nil
}

func parseQuickSearchRecord(contents []byte) (quickSearchRecord, error) {
	var fields []json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		return quickSearchRecord{}, err
	}
	if len(fields) < 5 {
		return quickSearchRecord{}, fmt.Errorf("expected at least five fields, got %d", len(fields))
	}
	brandID, err := rawInt(fields[0])
	if err != nil {
		return quickSearchRecord{}, fmt.Errorf("brand ID: %w", err)
	}
	id, err := rawInt(fields[1])
	if err != nil {
		return quickSearchRecord{}, fmt.Errorf("phone ID: %w", err)
	}
	model, err := rawString(fields[2])
	if err != nil {
		return quickSearchRecord{}, fmt.Errorf("model: %w", err)
	}
	aliases, err := rawStrings(fields[3])
	if err != nil {
		return quickSearchRecord{}, fmt.Errorf("aliases: %w", err)
	}
	image, err := rawString(fields[4])
	if err != nil {
		return quickSearchRecord{}, fmt.Errorf("image: %w", err)
	}
	display := ""
	if len(fields) > 5 {
		display, err = rawString(fields[5])
		if err != nil {
			return quickSearchRecord{}, fmt.Errorf("display name: %w", err)
		}
	}
	return quickSearchRecord{BrandID: brandID, ID: id, Model: strings.TrimSpace(model), Aliases: aliases, Image: strings.TrimSpace(image), Display: strings.TrimSpace(display)}, nil
}

func rawInt(contents json.RawMessage) (int64, error) {
	var number int64
	if json.Unmarshal(contents, &number) == nil {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(contents, &text); err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}

func rawString(contents json.RawMessage) (string, error) {
	if string(contents) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(contents, &text); err != nil {
		return "", err
	}
	return text, nil
}

func rawStrings(contents json.RawMessage) ([]string, error) {
	if string(contents) == "null" {
		return nil, nil
	}
	var text string
	if json.Unmarshal(contents, &text) == nil {
		if text == "" {
			return nil, nil
		}
		return []string{text}, nil
	}
	var values []string
	if err := json.Unmarshal(contents, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func parsePhonePage(contents []byte, fallback Phone, root string) (Phone, error) {
	if antiBotPage(contents) {
		return Phone{}, errors.New("page is an anti-bot challenge")
	}
	document, err := goquery.NewDocumentFromReader(bytes.NewReader(contents))
	if err != nil {
		return Phone{}, fmt.Errorf("parse HTML: %w", err)
	}
	specs := parseSpecs(document)
	if len(specs) == 0 {
		return Phone{}, errors.New("page did not contain phone specifications")
	}

	phone := fallback
	if name := cleanText(document.Find(`h1[data-spec="modelname"]`).First().Text()); name != "" {
		phone.Name = name
	} else if name := cleanText(document.Find(".specs-phone-name-title").First().Text()); name != "" {
		phone.Name = name
	} else if title := cleanText(document.Find("title").First().Text()); title != "" {
		phone.Name = strings.TrimSpace(strings.TrimSuffix(title, " - Full phone specifications"))
	}
	phone.Summary = cleanText(document.Find(`meta[name="Description"]`).First().AttrOr("content", ""))
	if phone.Summary == "" {
		phone.Summary = cleanText(document.Find(".specs-brief").First().Text())
	}

	canonical := document.Find(`link[rel="canonical"]`).First().AttrOr("href", "")
	historyURL := extractScriptValue(historyURLPattern, contents)
	pageID := int64(0)
	if value := extractScriptValue(historyIDPattern, contents); value != "" {
		pageID, _ = strconv.ParseInt(value, 10, 64)
	}
	if pageID == 0 {
		pageID = phoneIDFromSlug(slugFromURL(canonical))
	}
	if pageID == 0 {
		pageID = phoneIDFromSlug(slugFromURL(historyURL))
	}
	if phone.ID != 0 && pageID != 0 && phone.ID != pageID {
		return Phone{}, fmt.Errorf("page phone ID %d does not match requested ID %d", pageID, phone.ID)
	}
	if phone.ID == 0 {
		phone.ID = pageID
	}
	if phone.ID == 0 {
		return Phone{}, errors.New("page did not contain a phone ID")
	}
	if slug := firstNonEmpty(slugFromURL(canonical), slugFromURL(historyURL)); slug != "" {
		phone.Slug = slug
	}
	if phone.Slug == "" {
		phone.Slug = normalizeSlug(phone.Name) + "-" + strconv.FormatInt(phone.ID, 10)
	}
	phone.URL = root + phone.Slug + ".php"
	image := extractScriptValue(historyImagePattern, contents)
	if image == "" {
		image = document.Find(".specs-photo-main img").First().AttrOr("src", "")
	}
	if image == "" {
		image = document.Find(`meta[property="og:image"]`).First().AttrOr("content", "")
	}
	if image != "" {
		phone.Image = normalizeAssetURL(root, image)
	}
	phone.Specs = specs
	if phone.Name == "" {
		return Phone{}, errors.New("page did not contain a phone name")
	}
	return phone, nil
}

func parseSpecs(document *goquery.Document) []SpecSection {
	sections := make([]SpecSection, 0)
	document.Find("#specs-list table").Each(func(_ int, table *goquery.Selection) {
		name := cleanText(table.Find("th").First().Text())
		if name == "" {
			return
		}
		items := make([]SpecItem, 0)
		itemIndex := make(map[string]int)
		table.Find("tr").Each(func(_ int, row *goquery.Selection) {
			label := cleanText(row.Find("td.ttl").First().Text())
			values := selectionTexts(row.Find("td.nfo"))
			if label == "" {
				if len(items) > 0 {
					items[len(items)-1].Values = append(items[len(items)-1].Values, values...)
				}
				return
			}
			if len(values) == 0 {
				return
			}
			key := normalizeSearch(label)
			if index, found := itemIndex[key]; found {
				items[index].Values = append(items[index].Values, values...)
				return
			}
			itemIndex[key] = len(items)
			items = append(items, SpecItem{Name: label, Values: values})
		})
		if len(items) > 0 {
			sections = append(sections, SpecSection{Name: name, Items: items})
		}
	})
	return sections
}

func selectionTexts(selection *goquery.Selection) []string {
	values := make([]string, 0, selection.Length())
	selection.Each(func(_ int, item *goquery.Selection) {
		if value := cleanText(item.Text()); value != "" {
			values = append(values, value)
		}
	})
	return values
}

func antiBotPage(contents []byte) bool {
	lower := strings.ToLower(string(contents))
	for _, marker := range []string{"gsmarena turnstile check", "cf-turnstile", "cf-chl-", "challenge-platform", "just a moment...", "captcha"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func extractScriptValue(pattern *regexp.Regexp, contents []byte) string {
	match := pattern.FindSubmatch(contents)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

func slugFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(path.Base(strings.Trim(parsed.Path, "/")), ".php")
}

func normalizeAssetURL(root, raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if strings.HasPrefix(raw, "/") {
		return root + strings.TrimPrefix(raw, "/")
	}
	return raw
}

func phoneIDFromSlug(slug string) int64 {
	match := phoneIDSuffix.FindStringSubmatch(strings.TrimSuffix(slug, ".php"))
	if len(match) < 2 {
		return 0
	}
	id, _ := strconv.ParseInt(match[1], 10, 64)
	return id
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
