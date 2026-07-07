package main

import (
	"log"
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
// ID/mention handles are resolved by user id; plain "@username" text handles are
// resolved by searching the guild for that member (so their server nickname is
// shown). Both prefer the server nick → global name → username, are cached, and
// fall back to the raw handle text when the guild member can't be found. Slots
// with no handle are omitted so the roster falls back to its slot name / fill
// display.
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
		wg.Add(1)
		go func(slot int, handle string) {
			defer wg.Done()
			name := b.resolveHandleName(s, guildID, handle)
			if name == "" {
				return
			}
			mu.Lock()
			names[slot] = name
			mu.Unlock()
		}(p.Slot, h)
	}
	wg.Wait()
	return names
}

// resolveHandleName resolves a stored roster handle to a Discord display name,
// preferring the server nickname. An ID/mention handle is looked up by user id;
// a plain "@username" text handle is looked up by searching the guild for a
// member whose username or global name matches. Returns the raw username (minus
// a leading "@") as a fallback for a text handle that can't be matched, or ""
// for an unresolvable id handle (so the caller shows the raw handle).
func (b *bot) resolveHandleName(s *discordgo.Session, guildID, handle string) string {
	if id := discordIDFromHandle(handle); id != "" {
		return b.resolveMemberName(s, guildID, id)
	}
	username := strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if username == "" {
		return ""
	}
	if name := b.resolveUsernameName(s, guildID, username); name != "" {
		return name
	}
	// No matching guild member — show the username as before.
	return username
}

// resolveUsernameName finds a guild member by username (or global name) and
// returns their preferred display name (server nick → global name → username).
// Discord's member search is prefix-based, so the full username as the query
// returns the exact member among the results; we confirm with an exact
// (case-insensitive) match. Results are cached under a username key; a failed or
// unmatched search returns "" (and isn't cached, so it retries next render).
func (b *bot) resolveUsernameName(s *discordgo.Session, guildID, username string) string {
	if guildID == "" {
		return ""
	}
	key := guildID + ":@" + strings.ToLower(username)
	if name, ok := b.nameCache.get(key); ok {
		return name
	}
	members, err := s.GuildMembersSearch(guildID, username, 10)
	if err != nil {
		log.Printf("names: guild member search (guild %s, query %q): %v", guildID, username, err)
		return ""
	}
	for _, m := range members {
		if m == nil || m.User == nil {
			continue
		}
		if strings.EqualFold(m.User.Username, username) ||
			(m.User.GlobalName != "" && strings.EqualFold(m.User.GlobalName, username)) {
			if name := memberDisplayName(m); name != "" {
				b.nameCache.set(key, name)
				return name
			}
		}
	}
	return ""
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
		} else {
			log.Printf("names: guild member lookup (guild %s, user %s): %v", guildID, id, err)
		}
	}
	if name == "" {
		if u, err := s.User(id); err == nil {
			name = displayName(u)
		} else {
			log.Printf("names: user lookup (user %s): %v", id, err)
		}
	}
	if name != "" {
		b.nameCache.set(key, name)
	}
	return name
}

// discordIDFromHandle extracts a raw user ID from a stored handle when it is a
// mention (<@id> / <@!id>), a bare numeric ID, or an "@"-prefixed numeric ID
// (@id, how some handles get pasted); returns "" for "@username" text handles
// (which carry no resolvable ID).
func discordIDFromHandle(handle string) string {
	h := strings.TrimSpace(handle)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "<@") && strings.HasSuffix(h, ">") {
		h = strings.TrimSuffix(strings.TrimPrefix(h, "<@"), ">")
		h = strings.TrimPrefix(h, "!")
	}
	// A leading "@" on an otherwise-numeric handle (e.g. "@123456789012345678")
	// is a user ID, not a username — strip it so it resolves to a display name
	// rather than being shown verbatim as the id.
	h = strings.TrimPrefix(h, "@")
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

// interactionDisplayName returns the best display name for an interaction's
// invoker: their server nickname when present (guild interactions carry it on
// i.Member.Nick), else their global (display) name, else username. This is the
// name captured when a user signs up for a run or fills a post, so it reflects
// how they appear in the server. Falls back to the DM user when there's no
// member (e.g. a DM interaction).
func interactionDisplayName(i *discordgo.InteractionCreate) string {
	if i != nil && i.Member != nil {
		if n := memberDisplayName(i.Member); n != "" {
			return n
		}
	}
	if u := invokingUser(i); u != nil {
		return displayName(u)
	}
	return ""
}
