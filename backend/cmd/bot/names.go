package main

import (
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

// handleNameCache memoizes resolved Discord display names keyed by
// "guildID:userID" for a short TTL, so re-rendering a post on every RSVP/fill
// press doesn't re-hit the Discord API for each roster member.
type handleNameCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]nameCacheEntry
}

type nameCacheEntry struct {
	name string
	at   time.Time
}

func newHandleNameCache() *handleNameCache {
	return &handleNameCache{ttl: 10 * time.Minute, entries: map[string]nameCacheEntry{}}
}

func (c *handleNameCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.at) > c.ttl {
		return "", false
	}
	return e.name, true
}

func (c *handleNameCache) set(key, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = nameCacheEntry{name: name, at: time.Now()}
}

// resolveRosterNames maps each roster slot to the player's current Discord
// display name, resolved from their stored handle, for the post's roster.
// Mention/ID handles are looked up live (cached) via the guild member / user;
// plain "@username" text handles are shown as the username (no live identity is
// available without an ID). Slots with no handle are omitted so the roster falls
// back to its slot name / fill display.
func (b *bot) resolveRosterNames(s *discordgo.Session, guildID string, team *models.Team) map[int]string {
	names := map[int]string{}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, p := range team.Players {
		h := strings.TrimSpace(p.DiscordHandle)
		if h == "" {
			continue
		}
		id := discordIDFromHandle(h)
		if id == "" {
			// A plain text handle is already the username; drop the leading @.
			names[p.Slot] = strings.TrimPrefix(h, "@")
			continue
		}
		wg.Add(1)
		go func(slot int, id string) {
			defer wg.Done()
			name := b.resolveMemberName(s, guildID, id)
			if name == "" {
				return
			}
			mu.Lock()
			names[slot] = name
			mu.Unlock()
		}(p.Slot, id)
	}
	wg.Wait()
	return names
}

// resolveMemberName returns a Discord user's display name (guild nick, else
// global name, else username) for a raw user ID, preferring the guild member.
// Results are cached; lookups that fail return "" and aren't cached (so they
// retry on the next render).
func (b *bot) resolveMemberName(s *discordgo.Session, guildID, id string) string {
	key := guildID + ":" + id
	if name, ok := b.nameCache.get(key); ok {
		return name
	}
	name := ""
	if guildID != "" {
		if m, err := s.GuildMember(guildID, id); err == nil {
			name = memberDisplayName(m)
		}
	}
	if name == "" {
		if u, err := s.User(id); err == nil {
			name = displayName(u)
		}
	}
	if name != "" {
		b.nameCache.set(key, name)
	}
	return name
}

// discordIDFromHandle extracts a raw user ID from a stored handle when it is a
// mention (<@id> / <@!id>) or a bare numeric ID; returns "" for "@username"
// text handles (which carry no resolvable ID).
func discordIDFromHandle(handle string) string {
	h := strings.TrimSpace(handle)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "<@") && strings.HasSuffix(h, ">") {
		h = strings.TrimSuffix(strings.TrimPrefix(h, "<@"), ">")
		h = strings.TrimPrefix(h, "!")
	}
	if h == "" || !isAllDigits(h) {
		return ""
	}
	return h
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// memberDisplayName returns a guild member's preferred display name: server nick
// first, else the user's global (display) name, else username.
func memberDisplayName(m *discordgo.Member) string {
	if m == nil {
		return ""
	}
	if n := strings.TrimSpace(m.Nick); n != "" {
		return n
	}
	if m.User != nil {
		return displayName(m.User)
	}
	return ""
}
