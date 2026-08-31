package db

import "testing"

func TestStarboardAndSobboardCanStoreSameMessage(t *testing.T) {
	database, err := Open(t.TempDir() + "/metrobot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	for _, board := range []string{"starboard", "sobboard"} {
		if _, err := database.AddBoardEntry(board, "message", "channel", "guild", "author", "content", 4, 1); err != nil {
			t.Fatalf("AddBoardEntry(%q): %v", board, err)
		}
		entry, err := database.GetBoardEntry(board, "message")
		if err != nil {
			t.Fatalf("GetBoardEntry(%q): %v", board, err)
		}
		if entry == nil || entry.OriginalMsgID != "message" {
			t.Fatalf("GetBoardEntry(%q) = %#v", board, entry)
		}
	}
}
