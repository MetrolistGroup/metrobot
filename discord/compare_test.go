package discord

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MetrolistGroup/metrobot/db"
	"github.com/MetrolistGroup/metrobot/gsmarena"
	"github.com/MetrolistGroup/metrobot/nanoreview"
	"github.com/MetrolistGroup/metrobot/technicalcity"
)

func TestCompareCachedSourcesAndRows(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "compare.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for key, body := range map[string]string{
		"index":                  `{"brands":{"1":"Acme"},"records":[{"brand_id":1,"id":1,"model":"A","display":"A"},{"brand_id":1,"id":2,"model":"B","display":"B"}]}`,
		"phone:1":                `{"id":1,"name":"Acme A","url":"https://www.gsmarena.com/a-1.php","specs":[{"name":"Display","items":[{"name":"Size","values":["6 inches"]}]}]}`,
		"phone:2":                `{"id":2,"name":"Acme B","url":"https://www.gsmarena.com/b-2.php","specs":[{"name":"display","items":[{"name":"size","values":["7 inches"]},{"name":"Refresh","values":["120 Hz"]}]}]}`,
		"nano:detail:soc/chip-a": `{"name":"Chip A","slug":"soc/chip-a","url":"https://nanoreview.net/en/soc/chip-a","specs":[{"name":"CPU","items":[{"name":"Cores","values":["8"]}]}]}`,
		"nano:detail:soc/chip-b": `{"name":"Chip B","slug":"soc/chip-b","url":"https://nanoreview.net/en/soc/chip-b","specs":[{"name":"CPU","items":[{"name":"Cores","values":["10"]}]}]}`,
		"tc:device:gpu/Chip-A":   `{"name":"Chip A","slug":"gpu/Chip-A","url":"https://technical.city/en/gpu/Chip-A","specs":[{"name":"Memory","items":[{"name":"VRAM","values":["8 GB"]}]}]}`,
		"tc:device:gpu/Chip-B":   `{"name":"Chip B","slug":"gpu/Chip-B","url":"https://technical.city/en/gpu/Chip-B","specs":[{"name":"Memory","items":[{"name":"VRAM","values":["16 GB"]}]}]}`,
	} {
		if err := database.SetGSMArenaCache(key, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	bot := &Bot{gsmarena: gsmarena.New(database), nanoreview: nanoreview.New(database), technicalCity: technicalcity.New(database)}
	for _, pair := range []struct{ source, first, second, left, right string }{
		{"gsm", "1", "2", "6 inches", "7 inches"},
		{"nano", "soc/chip-a", "soc/chip-b", "8", "10"},
		{"tc", "gpu/Chip-A", "gpu/Chip-B", "8 GB", "16 GB"},
	} {
		first, err := bot.compareLookup(context.Background(), pair.source, pair.first)
		if err != nil {
			t.Fatal(err)
		}
		second, err := bot.compareLookup(context.Background(), pair.source, pair.second)
		if err != nil {
			t.Fatal(err)
		}
		rows := compareRows(first, second)
		if len(rows) == 0 || rows[0].first != pair.left || rows[0].second != pair.right {
			t.Fatalf("%s rows: %#v", pair.source, rows)
		}
		if pair.source == "gsm" && (len(rows) != 2 || rows[1].second != "120 Hz" || rows[1].first != "") {
			t.Fatalf("missing single-sided spec: %#v", rows)
		}
	}
}
