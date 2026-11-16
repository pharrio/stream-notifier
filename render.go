package main

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
)

const dashboardMarker = "stream-notifier-dashboard-v1"
const liveMarker = "stream-notifier-live-v1"
const iconURL = "https://raw.githubusercontent.com/Glimesh/glimesh.tv/1fb679f0dcc04154034a6b73e7b52ac2677493f1/priv/static/images/logos/logo-no-text.png"
const accentColor = 0x36468B // Deep blue from the left face of the official mark.

type Renderer struct {
	Config Config
	Text   Text
}

func (r Renderer) streamURL(key string) string {
	result := r.Config.PublicURL + "/" + url.PathEscape(key)
	if len(result) > 2048 {
		return ""
	}
	return result
}

func identity(key string, start time.Time) string {
	return fmt.Sprintf("%s|%d", key, start.UnixMilli())
}
func (r Renderer) liveID(s Stream) string { return identity(s.Key, s.Start) }
func identityKey(id string) string {
	if i := strings.LastIndexByte(id, '|'); i >= 0 {
		return id[:i]
	}
	return ""
}

func (r Renderer) Dashboard(states map[string]*LiveState, now, lastSuccess time.Time, complete bool) *discordgo.MessageEmbed {
	title := r.Text.Offline
	keys := []string{}
	for key, state := range states {
		if !state.Ghost && !state.Pending {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		title = r.Text.Online
	} else {
		for _, state := range states {
			if state.Pending {
				title = r.Text.Checking
				break
			}
		}
	}
	description := ""
	if !complete {
		title = r.Text.Unavailable
		last := r.Text.Never
		if !lastSuccess.IsZero() {
			last = fmt.Sprintf("<t:%d:R>", lastSuccess.Unix())
		}
		description = fmt.Sprintf(r.Text.Stale, last) + "\n\n"
	}
	for i, key := range keys {
		name := markdown(clip(key, 180))
		line := "- **" + name + "**\n"
		if link := r.streamURL(key); link != "" {
			line = "- [**" + name + "**](" + link + ")\n"
		}
		if textLen(description+line) > 3900 {
			description += fmt.Sprintf(r.Text.More, len(keys)-i)
			break
		}
		description += line
	}
	thumb := r.Config.Thumbnail
	if thumb == "" {
		thumb = iconURL
	}
	return &discordgo.MessageEmbed{Title: clip(title, 256), Description: clip(description, 4096), Color: accentColor, Timestamp: now.UTC().Format(time.RFC3339Nano), Footer: &discordgo.MessageEmbedFooter{Text: r.Text.Updated}, Thumbnail: &discordgo.MessageEmbedThumbnail{URL: thumb}}
}
func (r Renderer) Live(s Stream, now time.Time) *discordgo.MessageEmbed {
	link := r.streamURL(s.Key)
	return &discordgo.MessageEmbed{Title: clip(fmt.Sprintf(r.Text.Live, s.Key), 256), URL: link, Color: accentColor, Description: fmt.Sprintf(r.Text.Stats, duration(now.Sub(s.Start)), len(s.Sessions), s.Mbps()), Timestamp: s.Start.UTC().Format(time.RFC3339Nano), Footer: &discordgo.MessageEmbedFooter{Text: r.Text.Started}}
}
func duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	if total >= 3600 {
		return fmt.Sprintf("%dh %dm", total/3600, total%3600/60)
	}
	if total >= 60 {
		return fmt.Sprintf("%dm", total/60)
	}
	return fmt.Sprintf("%ds", total)
}
func textLen(s string) int { return len(utf16.Encode([]rune(s))) }
func clip(s string, limit int) string {
	if textLen(s) <= limit {
		return s
	}
	count := 0
	var b strings.Builder
	for _, r := range s {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if count+n > limit-1 {
			break
		}
		b.WriteRune(r)
		count += n
	}
	return b.String() + "…"
}
func markdown(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune("\\[]()*_~`|<>#", r) {
			b.WriteByte('\\')
		}
		if r == '\n' || r == '\r' {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Recover our dashboard by its unlinked, timestamped footer and live cards by watch URL.
// Older URL markers are still recognized so migration does not create duplicate cards.
func (r Renderer) classify(m *discordgo.Message, self string) (kind, id string) {
	if m.Author == nil || m.Author.ID != self || len(m.Embeds) == 0 || m.Embeds[0] == nil {
		return "", ""
	}
	e := m.Embeds[0]
	u, err := url.Parse(e.URL)
	if err != nil {
		return "", ""
	}
	if u.Fragment == dashboardMarker {
		return "dashboard", ""
	}
	markedLive := u.Fragment == liveMarker
	start, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return "", ""
	}
	kind = r.footerKind(e)
	if (markedLive || kind == "live") && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" {
		path := u.EscapedPath()
		key, err := url.PathUnescape(path[strings.LastIndexByte(path, '/')+1:])
		if err == nil && key != "" {
			return "live", identity(key, start)
		}
	}
	if e.URL == "" && kind == "dashboard" {
		return "dashboard", ""
	}
	return "", ""
}

func (r Renderer) footerKind(e *discordgo.MessageEmbed) string {
	if e.Footer == nil {
		return ""
	}
	texts := []Text{r.Text}
	for _, language := range []string{"en", "zh-CN"} {
		t, _ := loadText(language)
		texts = append(texts, t)
	}
	for _, t := range texts {
		if e.Footer.Text == t.Updated && t.Updated != "" {
			return "dashboard"
		}
		if e.Footer.Text == t.Started && t.Started != "" {
			return "live"
		}
	}
	return ""
}
