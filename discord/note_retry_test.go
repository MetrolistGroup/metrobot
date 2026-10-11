package discord

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/disgoorg/disgo/rest"
)

func TestNoteRetriesOnlyConfirmedRejections(t *testing.T) {
	for _, test := range []struct {
		err   error
		retry bool
	}{
		{nil, false}, {context.DeadlineExceeded, false}, {fmt.Errorf("lost response"), false},
		{&rest.Error{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, false},
		{&rest.Error{Response: &http.Response{StatusCode: http.StatusForbidden}}, false},
		{fmt.Errorf("wrapped: %w", &rest.Error{Response: &http.Response{StatusCode: http.StatusBadRequest}}), true},
		{&rest.Error{Response: &http.Response{StatusCode: http.StatusNotFound}}, true},
	} {
		if got := noteRequestRejected(test.err); got != test.retry {
			t.Fatalf("retry %v = %v, want %v", test.err, got, test.retry)
		}
	}
}
