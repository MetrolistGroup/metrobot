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

func TestCompareVerdict(t *testing.T) {
	for _, tt := range []struct {
		section, name, first, second string
		winner                       int
	}{
		{"Battery", "Type", "Li-Ion 5060 mAh", "Li-Po 4500 mAh", 1},
		{"Body", "Weight", "221 g (7.80 oz)", "187 g (6.60 oz)", 2},
		{"CPU", "Process", "3 nanometers", "4 nanometers", 1},
		{"GeekBench 6", "Photo processing", "98.7 images/sec", "120 images/sec", 2},
		{"AnTuTu 11", "CPU", "1112833", "998,000", 1},
		{"Primary details", "Place in the ranking", "1333", "120", 2},
		{"Our Tests", "Loudspeaker", "-25.5 LUFS (Very good)", "-23 LUFS", 2},
		{"Memory", "Max size", "24 GB", "16 GB", 1},
		{"Display", "Size", "6.8 inches", "6.1 inches", 0},
		{"Platform", "OS", "Android 14", "iOS 18", 0},
		{"Platform", "Chipset", "Google Tensor G4 (4 nm)", "Apple A18 (3 nm)", 0},
		{"Primary details", "Release date", "30 April 2020", "2 May 2021", 0},
		{"Memory specs", "Supported memory types", "DDR4-2666", "DDR5-5600", 0},
		{"Detailed specifications", "L3 cache", "12 MB (shared)", "512 KB", 0},
		{"Connectivity", "Wi-Fi", "7", "7", 3},
		{"Display", "Response time", "4.7 ms", "9.4 ms", 1},
		{"Network", "Latency", "40 ms", "25 ms", 2},
		{"Battery", "Charging time", "1 h", "2 h", 1},
		{"Battery", "Battery life", "20 h", "25 h", 2},
		{"Display", "PWM", "240 Hz", "120 Hz", 1},
		{"Camera", "Shutter lag", "120 ms", "80 ms", 0},
	} {
		if got, _ := compareVerdict(tt.section, tt.name, tt.first, tt.second); got != tt.winner {
			t.Errorf("%s · %s: %q vs %q = %d, want %d", tt.section, tt.name, tt.first, tt.second, got, tt.winner)
		}
	}
	if _, lead := compareVerdict("CPU", "Cores", "12", "15"); lead != 25 {
		t.Errorf("lead = %v, want 25", lead)
	}
	if _, lead := compareVerdict("Comms", "USB", "USB Type-C 3.2", "USB Type-C 3.0"); lead != 0 {
		t.Errorf("version lead = %v, want 0", lead)
	}
}
