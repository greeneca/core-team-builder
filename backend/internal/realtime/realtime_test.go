package realtime

import (
	"sort"
	"testing"
	"time"
)

// recv reads one event, failing the test rather than hanging forever if the hub
// never delivers.
func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed, want an event")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}
	}
}

// drainPresence consumes the presence event Subscribe always emits, so a test
// can then assert on the next event it actually cares about.
func drainPresence(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	ev := recv(t, ch)
	if ev.Kind != KindPresence {
		t.Fatalf("first event kind = %q, want %q", ev.Kind, KindPresence)
	}
	return ev
}

func TestSubscribeAnnouncesPresence(t *testing.T) {
	h := NewHub()

	ch, unsubscribe := h.Subscribe(7, "alice")
	defer unsubscribe()

	ev := drainPresence(t, ch)
	if ev.TeamID != 7 {
		t.Errorf("TeamID = %d, want 7", ev.TeamID)
	}
	if len(ev.Users) != 1 || ev.Users[0] != "alice" {
		t.Errorf("Users = %v, want [alice]", ev.Users)
	}
}

// TestPresenceUpdatesOnJoinAndLeave is the behaviour the "who's viewing this
// team" indicator depends on: every membership change re-broadcasts the list.
func TestPresenceUpdatesOnJoinAndLeave(t *testing.T) {
	h := NewHub()

	aliceCh, aliceOff := h.Subscribe(1, "alice")
	defer aliceOff()
	drainPresence(t, aliceCh)

	_, bobOff := h.Subscribe(1, "bob")

	ev := drainPresence(t, aliceCh)
	got := append([]string(nil), ev.Users...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("after bob joined, Users = %v, want [alice bob]", got)
	}

	bobOff()

	ev = drainPresence(t, aliceCh)
	if len(ev.Users) != 1 || ev.Users[0] != "alice" {
		t.Errorf("after bob left, Users = %v, want [alice]", ev.Users)
	}
}

// TestPresenceKeepsDuplicateUsernames documents the contract with the client:
// one user with several tabs appears once per connection, and de-duplication is
// the client's job.
func TestPresenceKeepsDuplicateUsernames(t *testing.T) {
	h := NewHub()

	ch, off1 := h.Subscribe(1, "alice")
	defer off1()
	drainPresence(t, ch)

	_, off2 := h.Subscribe(1, "alice")
	defer off2()

	ev := drainPresence(t, ch)
	if len(ev.Users) != 2 {
		t.Errorf("Users = %v, want alice twice (one entry per connection)", ev.Users)
	}
}

func TestPublishReachesSubscribers(t *testing.T) {
	h := NewHub()

	ch, off := h.Subscribe(42, "alice")
	defer off()
	drainPresence(t, ch)

	h.Publish(Event{TeamID: 42, Kind: "encounter"})

	ev := recv(t, ch)
	if ev.Kind != "encounter" || ev.TeamID != 42 {
		t.Errorf("got %+v, want an encounter event for team 42", ev)
	}
}

// TestPublishIsScopedToOneTeam is a correctness *and* privacy property: a
// change to one team must not be delivered to viewers of another.
func TestPublishIsScopedToOneTeam(t *testing.T) {
	h := NewHub()

	oneCh, oneOff := h.Subscribe(1, "alice")
	defer oneOff()
	drainPresence(t, oneCh)

	twoCh, twoOff := h.Subscribe(2, "bob")
	defer twoOff()
	drainPresence(t, twoCh)

	h.Publish(Event{TeamID: 1, Kind: "team"})

	if ev := recv(t, oneCh); ev.Kind != "team" {
		t.Errorf("team 1 subscriber got %+v", ev)
	}
	select {
	case ev := <-twoCh:
		t.Errorf("team 2 subscriber should have received nothing, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPublishToATeamWithNoSubscribersIsANoop(t *testing.T) {
	h := NewHub()
	h.Publish(Event{TeamID: 999, Kind: "team"}) // must not panic
}

// TestUnsubscribeClosesTheChannel lets the SSE handler's range loop terminate.
func TestUnsubscribeClosesTheChannel(t *testing.T) {
	h := NewHub()

	ch, off := h.Subscribe(1, "alice")
	drainPresence(t, ch)
	off()

	// Drain the final presence broadcast, if the implementation emitted one
	// before closing, then require the channel to end.
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed, as required
			}
		case <-time.After(2 * time.Second):
			t.Fatal("channel was never closed after unsubscribe")
		}
	}
}

// TestUnsubscribeIsIdempotent matters because the SSE handler defers it and may
// also call it on an error path; a second close would panic.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	h := NewHub()

	_, off := h.Subscribe(1, "alice")
	off()
	off()
	off()
}

// TestUnsubscribeStopsDelivery also covers the hub forgetting an empty team,
// so a long-lived server doesn't accumulate entries for teams nobody watches.
func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()

	_, off := h.Subscribe(5, "alice")
	off()

	h.Publish(Event{TeamID: 5, Kind: "team"}) // must not panic on a closed channel

	h.mu.Lock()
	_, stillTracked := h.subs[5]
	h.mu.Unlock()
	if stillTracked {
		t.Error("hub should forget a team once its last subscriber leaves")
	}
}

// TestSlowSubscriberIsSkipped is the property that keeps one stalled browser
// from blocking everyone else: delivery is best-effort past the buffer.
func TestSlowSubscriberIsSkipped(t *testing.T) {
	h := NewHub()

	slowCh, slowOff := h.Subscribe(1, "slow")
	defer slowOff()
	fastCh, fastOff := h.Subscribe(1, "fast")
	defer fastOff()

	// Neither channel is being read, so both fill up. Publishing well past the
	// 16-event buffer must still return promptly rather than block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			h.Publish(Event{TeamID: 1, Kind: "team"})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}

	// Both still hold their buffered events; nothing was lost for the reader
	// that does keep up within the buffer.
	if len(slowCh) == 0 || len(fastCh) == 0 {
		t.Errorf("buffers = slow:%d fast:%d, want both to hold events", len(slowCh), len(fastCh))
	}
}
