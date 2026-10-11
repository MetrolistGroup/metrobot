package auditsearch

import (
	"context"
	"github.com/disgoorg/disgo/discord"
	"testing"
)

func TestLogSearchUnicodeCaseFolding(t *testing.T) {
	for _, content := range []string{"ος", "Straße"} {
		q := dayQuery()
		if content == "ος" {
			q.Text = "ΟΣ"
		} else {
			q.Text = "STRASSE"
		}
		result, err := New(&historyMock{messages: []discord.Message{logMessage(q.From, content)}}).Search(context.Background(), q)
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("%q did not match %q: %+v, %v", q.Text, content, result, err)
		}
	}
}
