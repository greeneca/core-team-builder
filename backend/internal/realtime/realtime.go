// Package realtime delivers team-change notifications to connected browsers.
//
// Any process that writes to a collaborative table fires a Postgres trigger
// (see migration 044) that NOTIFYs the "team_changed" channel. A single Hub in
// the API server LISTENs on that channel and fans each change out to the
// Server-Sent Events connections subscribed to that team — so an edit made by
// one user (or by the Discord bot, a separate process) refreshes everyone
// else's view. The Hub also tracks who is connected per team and broadcasts a
// presence event whenever that set changes.
package realtime

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// channel is the Postgres NOTIFY channel the triggers publish on.
const channel = "team_changed"

// Kinds carried by an Event. Change kinds tell a client which area to refresh;
// KindPresence carries the list of connected viewers instead.
const (
	KindPresence = "presence"
)

// Event is one message delivered to a team's subscribers: either a change
// notification (Kind is the area that changed, e.g. "team"/"encounter") or a
// presence update (Kind == KindPresence, Users lists the connected viewers).
type Event struct {
	TeamID int64    `json:"team_id"`
	Kind   string   `json:"kind"`
	Users  []string `json:"users,omitempty"`
}

// subscriber is one connected SSE client for a team.
type subscriber struct {
	id       int64
	username string
	ch       chan Event
}

// Hub fans team-change and presence events out to subscribed SSE clients. It is
// safe for concurrent use.
type Hub struct {
	mu     sync.Mutex
	nextID int64
	subs   map[int64]map[int64]*subscriber // teamID -> subscriberID -> subscriber
}

// NewHub constructs an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: map[int64]map[int64]*subscriber{}}
}

// Subscribe registers a viewer (username) for a team's events. It returns the
// event channel to read from and an unsubscribe func the caller MUST invoke
// when the connection ends. Both subscribing and unsubscribing broadcast an
// updated presence event to the team.
func (h *Hub) Subscribe(teamID int64, username string) (<-chan Event, func()) {
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	sub := &subscriber{id: id, username: username, ch: make(chan Event, 16)}
	if h.subs[teamID] == nil {
		h.subs[teamID] = map[int64]*subscriber{}
	}
	h.subs[teamID][id] = sub
	h.mu.Unlock()

	h.broadcastPresence(teamID)

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			if m := h.subs[teamID]; m != nil {
				delete(m, id)
				if len(m) == 0 {
					delete(h.subs, teamID)
				}
			}
			close(sub.ch)
			h.mu.Unlock()
			h.broadcastPresence(teamID)
		})
	}
	return sub.ch, unsubscribe
}

// Publish delivers a change event to every subscriber of its team. Delivery is
// non-blocking: a subscriber whose buffer is full is skipped (it will catch up
// on the next event or on reconnect), so one slow client can't stall the rest.
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subs[ev.TeamID] {
		select {
		case sub.ch <- ev:
		default:
		}
	}
}

// broadcastPresence sends the current viewer list for a team to its
// subscribers. Usernames may repeat (one user with several tabs); clients
// de-duplicate for display.
func (h *Hub) broadcastPresence(teamID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	subs := h.subs[teamID]
	users := make([]string, 0, len(subs))
	for _, s := range subs {
		users = append(users, s.username)
	}
	ev := Event{TeamID: teamID, Kind: KindPresence, Users: users}
	for _, s := range subs {
		select {
		case s.ch <- ev:
		default:
		}
	}
}

// Listen runs the Postgres LISTEN loop until ctx is cancelled, publishing every
// "team_changed" notification to the Hub. It uses its own dedicated connection
// (not a pooled one, which must not carry a long-lived LISTEN) and self-heals by
// reconnecting after a short backoff if the connection drops. Run in a goroutine.
func (h *Hub) Listen(ctx context.Context, databaseURL string) {
	for {
		if err := h.listenOnce(ctx, databaseURL); err != nil && ctx.Err() == nil {
			log.Printf("realtime: listen error: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context, databaseURL string) error {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var ev Event
		if err := json.Unmarshal([]byte(n.Payload), &ev); err != nil {
			log.Printf("realtime: bad notification payload %q: %v", n.Payload, err)
			continue
		}
		h.Publish(ev)
	}
}
