package technicalcity

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/PuerkitoBio/goquery"
)

func document(body []byte) (*goquery.Document, error) {
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"cf-chl-", "cf-turnstile", "challenge-platform", "just a moment...", "verify you are human", "<title>access denied"} {
		if strings.Contains(lower, marker) {
			return nil, errors.New("Technical City returned an anti-bot challenge")
		}
	}
	return goquery.NewDocumentFromReader(bytes.NewReader(body))
}

func parseSearch(body []byte) ([]Device, error) {
	doc, err := document(body)
	if err != nil {
		return nil, err
	}
	heading := doc.Find(".head-title").First()
	if cleanText(heading.Find("h1").Text()) != "Search results:" {
		return nil, errors.New("Technical City did not return a search results page")
	}
	results := heading.NextFiltered("span")
	devices := make([]Device, 0)
	seen := make(map[string]bool)
	results.Find(".comparing .item > a[href]").Each(func(_ int, a *goquery.Selection) {
		device, err := detailTarget(a.AttrOr("href", ""))
		if err != nil || seen[device.Slug] {
			return
		}
		label := a.Find(".type").First().Clone()
		label.Find("em").BeforeHtml(" ")
		device.Name = cleanText(label.Text())
		if device.Name == "" {
			return
		}
		device.Image = imageURL(a.Find("img").First().AttrOr("src", ""))
		seen[device.Slug] = true
		devices = append(devices, device)
	})
	if len(devices) == 0 && !strings.Contains(results.Text(), "Nothing has been found.") {
		return nil, errors.New("Technical City search contained no recognizable device results")
	}
	return devices, nil
}

func parseDevice(body []byte, requested Device) (Device, error) {
	doc, err := document(body)
	if err != nil {
		return Device{}, err
	}
	device, err := detailTarget(doc.Find(`link[rel="canonical"]`).First().AttrOr("href", ""))
	if err != nil || !strings.EqualFold(device.Slug, requested.Slug) {
		return Device{}, fmt.Errorf("Technical City page does not match requested device %q", requested.Slug)
	}
	device.Name = strings.TrimSuffix(cleanText(doc.Find(".head-title h1").First().Text()), ": specs and benchmarks")
	device.Image = imageURL(doc.Find(".autocomplete-select.selected img").First().AttrOr("src", ""))
	device.Summary = cleanText(doc.Find(`meta[name="description"]`).First().AttrOr("content", ""))
	doc.Find("h2").Each(func(_ int, heading *goquery.Selection) {
		if cleanText(heading.Text()) == "Summary" {
			var paragraphs []string
			heading.NextUntil("h2").Filter("p").Each(func(_ int, p *goquery.Selection) {
				paragraphs = append(paragraphs, cleanText(p.Text()))
			})
			if len(paragraphs) > 0 {
				device.Summary = strings.Join(paragraphs, " ")
			}
		}
	})
	// Specification tables have an explanatory notice; gaming/comparison tables do not.
	doc.Find("p.item_data_notice + table.compare-table").Each(func(_ int, table *goquery.Selection) {
		section := gsmarena.SpecSection{Name: cleanText(table.PrevAllFiltered("h2").First().Text())}
		if section.Name == "" {
			return
		}
		table.Find("tr").Each(func(_ int, row *goquery.Selection) {
			cells := row.ChildrenFiltered("td")
			name, value := cleanText(cells.Eq(0).Text()), cleanText(cells.Eq(1).Text())
			// The third cell describes another device's record, not this device's spec.
			if name != "" && value != "" {
				section.Items = append(section.Items, gsmarena.SpecItem{Name: name, Values: []string{value}})
			}
		})
		if len(section.Items) > 0 {
			device.Specs = append(device.Specs, section)
		}
	})
	if device.Name == "" || len(device.Specs) == 0 {
		return Device{}, errors.New("Technical City page contained no device name or specifications")
	}
	return device, nil
}

func cleanText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func searchText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

func imageURL(value string) string {
	if value == "" {
		return ""
	}
	root, _ := url.Parse(baseURL)
	u, err := root.Parse(value)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "technical.city") || u.User != nil {
		return ""
	}
	return u.String()
}
