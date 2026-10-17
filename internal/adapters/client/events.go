package client

import (
	"context"
	"encoding/json"
	"net/http"
)

type EventResource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type EventNotification struct {
	LibraryID   string         `json:"library_id"`
	StreamEpoch string         `json:"stream_epoch"`
	Sequence    string         `json:"sequence"`
	Type        string         `json:"type"`
	Resource    *EventResource `json:"resource"`
	Revision    int64          `json:"revision"`
}
type EventResync struct {
	Type         string `json:"type"`
	Reason       string `json:"reason"`
	StreamEpoch  string `json:"stream_epoch"`
	Head         string `json:"head"`
	ResumeCursor string `json:"resume_cursor"`
}
type EventPage struct {
	Items       []EventNotification `json:"items"`
	StreamEpoch string              `json:"stream_epoch"`
	Head        string              `json:"head"`
	NextCursor  string              `json:"next_cursor"`
	Resync      *EventResync        `json:"resync"`
}

// PollEvents reads confirmed notifications only. A resync control is returned to
// the caller, which must fetch fresh authorized state before replaying from its
// head. Polling never acknowledges, retries or changes a pending local write.
func (c *Client) PollEvents(ctx context.Context, cursor string) (EventPage, error) {
	var page EventPage
	headers := map[string]string{}
	if cursor != "" {
		headers["Last-Event-ID"] = cursor
	}
	response, err := c.Call(ctx, http.MethodGet, "/api/v1/events?transport=poll", "", nil, headers)
	if err != nil {
		return page, err
	}
	defer response.Body.Close()
	var envelope struct {
		Data EventPage `json:"data"`
	}
	err = json.NewDecoder(response.Body).Decode(&envelope)
	return envelope.Data, err
}
