package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Token      string
	Channels   []string
	LogChannel string
	PublicURL  string
	APIURL     string
	Language   string
	Thumbnail  string
	Interval   time.Duration
}

func loadConfig(get func(string) string) (Config, error) {
	c := Config{Token: strings.TrimSpace(get("DISCORD_TOKEN")), PublicURL: strings.TrimRight(strings.TrimSpace(get("BROADCAST_BOX_PUBLIC_URL")), "/"), Language: strings.TrimSpace(get("LANGUAGE")), Interval: 10 * time.Second}
	if c.Token == "" {
		return c, fmt.Errorf("DISCORD_TOKEN is required")
	}
	if err := validateURL(c.PublicURL); err != nil {
		return c, fmt.Errorf("BROADCAST_BOX_PUBLIC_URL: %w", err)
	}
	seen := map[string]bool{}
	for _, value := range strings.Split(get("CHANNEL_IDS"), ",") {
		id := strings.TrimSpace(value)
		if id == "" {
			continue
		}
		if !validID(id) {
			return c, fmt.Errorf("CHANNEL_IDS must contain positive Discord IDs")
		}
		if !seen[id] {
			c.Channels = append(c.Channels, id)
			seen[id] = true
		}
	}
	if len(c.Channels) == 0 {
		return c, fmt.Errorf("CHANNEL_IDS is required")
	}
	c.LogChannel = strings.TrimSpace(get("LOG_CHANNEL_ID"))
	if c.LogChannel != "" && (!validID(c.LogChannel) || seen[c.LogChannel]) {
		return c, fmt.Errorf("LOG_CHANNEL_ID must be a valid ID distinct from notification channels")
	}
	c.APIURL = strings.TrimRight(strings.TrimSpace(get("BROADCAST_BOX_API_URL")), "/")
	if c.APIURL == "" {
		c.APIURL = c.PublicURL
	}
	if err := validateURL(c.APIURL); err != nil {
		return c, fmt.Errorf("BROADCAST_BOX_API_URL: %w", err)
	}
	if c.Language == "" {
		c.Language = "en"
	}
	if c.Language != "en" && c.Language != "zh-CN" {
		return c, fmt.Errorf("LANGUAGE must be en or zh-CN")
	}
	c.Thumbnail = strings.TrimSpace(get("DASHBOARD_THUMBNAIL_URL"))
	if c.Thumbnail != "" {
		if err := validateImageURL(c.Thumbnail); err != nil {
			return c, fmt.Errorf("DASHBOARD_THUMBNAIL_URL: %w", err)
		}
	}
	if raw := strings.TrimSpace(get("CHECK_INTERVAL_SECONDS")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 5 || seconds > 3600 {
			return c, fmt.Errorf("CHECK_INTERVAL_SECONDS must be between 5 and 3600")
		}
		c.Interval = time.Duration(seconds) * time.Second
	}
	return c, nil
}

func validID(id string) bool {
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(id, 10, 64)
	return err == nil && n > 0
}

func validateURL(raw string) error {
	return validateHTTPURL(raw, false)
}

func validateImageURL(raw string) error {
	return validateHTTPURL(raw, true)
}

func validateHTTPURL(raw string, image bool) error {
	u, err := url.Parse(raw)
	limit := 1500
	if image {
		limit = 2048
	}
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (!image && (u.RawQuery != "" || u.ForceQuery)) || u.Fragment != "" || strings.ContainsAny(raw, " \t\r\n") || len(raw) > limit {
		return fmt.Errorf("expected a valid HTTP(S) URL without credentials or fragment; base URLs cannot have queries")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid port")
		}
	}
	return nil
}
