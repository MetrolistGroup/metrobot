package nanoreview

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/PuerkitoBio/goquery"
)

func parseSearch(body []byte) ([]Device, error) {
	var records []struct {
		Slug  string `json:"slug"`
		Label string `json:"label"`
		Name  string `json:"name"`
		Type  string `json:"content_type"`
	}
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("decode NanoReview search: %w", err)
	}
	if records == nil {
		return nil, errors.New("NanoReview search did not return an array")
	}
	devices := make([]Device, 0, len(records))
	seen := make(map[string]bool)
	for _, record := range records {
		device, err := detailTarget(record.Type + "/" + record.Slug)
		if err != nil {
			continue
		}
		device.Name = cleanText(record.Label)
		if device.Name == "" {
			device.Name = cleanText(record.Name)
		}
		if device.Name == "" || seen[device.Slug] {
			continue
		}
		seen[device.Slug] = true
		devices = append(devices, device)
		if len(devices) == maxResults {
			break
		}
	}
	if len(records) > 0 && len(devices) == 0 {
		return nil, errors.New("NanoReview search returned no supported product metadata")
	}
	return devices, nil
}

func parseDevice(body []byte, device Device) (Device, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return Device{}, fmt.Errorf("parse NanoReview HTML: %w", err)
	}
	if doc.Find(`#challenge-form, script[src*="challenge-platform"]`).Length() > 0 || strings.Contains(strings.ToLower(doc.Find("title").Text()), "just a moment") {
		return Device{}, errors.New("NanoReview returned an anti-bot challenge")
	}
	if canonical := doc.Find(`link[rel="canonical"]`).AttrOr("href", ""); canonical != "" {
		actual, err := detailTarget(canonical)
		if err != nil || actual.Slug != device.Slug {
			return Device{}, errors.New("NanoReview canonical URL does not match requested device")
		}
	}
	device.Name = cleanText(doc.Find("#the-app h1").First().Text())
	device.Summary = cleanText(doc.Find(`meta[name="description"], meta[property="og:description"]`).First().AttrOr("content", ""))
	image := doc.Find("#the-app .chip-top img").First().AttrOr("src", "")
	if image == "" {
		image = doc.Find(`meta[property="og:image"]`).AttrOr("content", "")
	}
	if u, err := url.Parse(image); err == nil && image != "" {
		root, _ := url.Parse(baseURL)
		u = root.ResolveReference(u)
		// Images are metadata only and are never fetched by this client.
		if u.Scheme == "https" && u.User == nil && u.Host == "nanoreview.net" {
			device.Image = u.String()
		}
	}
	// All five product categories use heading-bearing cards and specs tables.
	// Include benchmark tables too; omit configuration controls and review scores.
	doc.Find("#the-app .card").Each(func(_ int, card *goquery.Selection) {
		section := gsmarena.SpecSection{Name: cleanText(card.Find("h2, h3").First().Text())}
		if section.Name == "" {
			return
		}
		card.Find("table.specs-table tr").Each(func(_ int, row *goquery.Selection) {
			name := cleanText(row.Find("td.cell-h").First().Text())
			if name == "" {
				return
			}
			var values []string
			row.Find("td.cell-s").Each(func(_ int, cell *goquery.Selection) {
				cell = cell.Clone()
				cell.Find("script, style, select, button").Remove()
				cell.Find("br").ReplaceWithHtml("\n")
				for _, line := range strings.Split(cell.Text(), "\n") {
					if value := cleanText(line); value != "" {
						values = append(values, value)
					}
				}
			})
			if len(values) > 0 {
				section.Items = append(section.Items, gsmarena.SpecItem{Name: name, Values: values})
			}
		})
		if len(section.Items) > 0 {
			device.Specs = append(device.Specs, section)
		}
	})
	if device.Name == "" || len(device.Specs) == 0 {
		return Device{}, errors.New("NanoReview page did not contain a product name and specifications")
	}
	return device, nil
}

func cleanText(value string) string { return strings.Join(strings.Fields(value), " ") }
