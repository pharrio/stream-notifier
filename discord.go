package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

var errNotFound = errors.New("Discord message not found")

type History struct {
	Messages []*discordgo.Message
	Cursor   string
}

type Messenger interface {
	History(context.Context, string, string, string) (History, error)
	Send(context.Context, string, *discordgo.MessageEmbed) (*discordgo.Message, error)
	Edit(context.Context, string, string, *discordgo.MessageEmbed) error
	Delete(context.Context, string, string) error
	Notice(context.Context, string, string) error
}

type Discord struct {
	Session *discordgo.Session
	gate    chan struct{}
}

func newDiscord(session *discordgo.Session) *Discord {
	return &Discord{Session: session, gate: make(chan struct{}, 1)}
}

// DiscordGo's limiter can sleep outside HTTP/context deadlines. Keep its rate-limit
// handling, but bound the caller's wait and allow at most one outstanding SDK call.
func discordCall[T any](d *Discord, parent context.Context, call func(context.Context) (T, error)) (T, error) {
	var zero T
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	select {
	case d.gate <- struct{}{}:
	case <-ctx.Done():
		return zero, ctx.Err()
	}
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-d.gate }()
		value, err := call(ctx)
		done <- result{value, err}
	}()
	select {
	case r := <-done:
		return r.value, r.err
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (d *Discord) Self(ctx context.Context) (*discordgo.User, error) {
	user, err := discordCall(d, ctx, func(c context.Context) (*discordgo.User, error) {
		return d.Session.User("@me", discordgo.WithContext(c))
	})
	if err == nil && user == nil {
		return nil, errors.New("Discord returned an empty user")
	}
	return user, discordError(err)
}

func discordError(err error) error {
	if err == nil {
		return nil
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil {
		if rest.Response.StatusCode == http.StatusNotFound && rest.Message != nil && rest.Message.Code == 10008 {
			return errNotFound
		}
		detail := fmt.Sprintf("Discord HTTP %d", rest.Response.StatusCode)
		if rest.Message != nil {
			detail += fmt.Sprintf(" (code %d)", rest.Message.Code)
		}
		if fields := errorFields(rest.ResponseBody); len(fields) > 0 {
			detail += "; fields: " + strings.Join(fields, ", ")
		}
		return errors.New(detail)
	}
	return fmt.Errorf("Discord request: %w", networkError(err))
}

// Only report schema field paths, never response messages or rejected values.
func errorFields(body []byte) []string {
	if len(body) > 8192 {
		return nil
	}
	var envelope struct {
		Errors map[string]json.RawMessage `json:"errors"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("embeds components allowed_mentions content attachments thumbnail url timestamp title description footer text color image type fields name value inline custom_id") {
		allowed[key] = true
	}
	paths := []string{}
	var walk func(map[string]json.RawMessage, string)
	walk = func(obj map[string]json.RawMessage, path string) {
		if len(paths) >= 8 {
			return
		}
		if _, ok := obj["_errors"]; ok && path != "" {
			paths = append(paths, path)
		}
		keys := make([]string, 0, len(obj))
		for key := range obj {
			if key != "_errors" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !allowed[key] {
				if _, err := strconv.ParseUint(key, 10, 16); err != nil {
					continue
				}
			}
			var child map[string]json.RawMessage
			if json.Unmarshal(obj[key], &child) != nil || child == nil {
				continue
			}
			next := key
			if path != "" {
				next = path + "." + key
			}
			if len(next) <= 200 {
				walk(child, next)
			}
		}
	}
	walk(envelope.Errors, "")
	return paths
}

func networkError(err error) error {
	var unknownCA x509.UnknownAuthorityError
	var invalidCert x509.CertificateInvalidError
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.As(err, &unknownCA), errors.As(err, &invalidCert):
		return errors.New("TLS certificate verification failed (check readable CA certificates and system clock)")
	case errors.As(err, &dns):
		return errors.New("DNS resolution failed")
	case errors.As(err, &network) && network.Timeout():
		return errors.New("request timed out")
	case errors.Is(err, context.Canceled):
		return errors.New("request canceled")
	default:
		return errors.New("connection failed")
	}
}

func newerID(a, b string) bool {
	x, _ := strconv.ParseUint(a, 10, 64)
	y, _ := strconv.ParseUint(b, 10, 64)
	return x > y
}

// Page newest-first until the saved cursor. Keep only this bot's messages in memory,
// while advancing over all authors so ordinary conversation is not scanned again.
func (d *Discord) History(ctx context.Context, channel, after, author string) (History, error) {
	result := History{Cursor: after}
	before := ""
	for {
		page, err := discordCall(d, ctx, func(c context.Context) ([]*discordgo.Message, error) {
			return d.Session.ChannelMessages(channel, 100, before, "", "", discordgo.WithContext(c))
		})
		if err != nil {
			return History{}, discordError(err)
		}
		reached := false
		for _, m := range page {
			if m == nil {
				continue
			}
			if after != "" && !newerID(m.ID, after) {
				reached = true
				continue
			}
			if newerID(m.ID, result.Cursor) {
				result.Cursor = m.ID
			}
			if m.Author != nil && m.Author.ID == author {
				result.Messages = append(result.Messages, m)
			}
		}
		if reached || len(page) < 100 {
			return result, nil
		}
		next := page[len(page)-1].ID
		if next == before {
			return History{}, errors.New("Discord history pagination did not advance")
		}
		before = next
	}
}
func (d *Discord) Send(ctx context.Context, channel string, e *discordgo.MessageEmbed) (*discordgo.Message, error) {
	prepared := *e
	m, err := discordCall(d, ctx, func(c context.Context) (*discordgo.Message, error) {
		return d.Session.ChannelMessageSendComplex(channel, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{&prepared}, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(c))
	})
	if err == nil && (m == nil || m.ID == "") {
		return nil, errors.New("Discord returned an empty message; history recovery is required")
	}
	return m, discordError(err)
}
func (d *Discord) Edit(ctx context.Context, channel, id string, e *discordgo.MessageEmbed) error {
	prepared := *e
	embeds := []*discordgo.MessageEmbed{&prepared}
	empty := ""
	components := []discordgo.MessageComponent{}
	_, err := discordCall(d, ctx, func(c context.Context) (*discordgo.Message, error) {
		return d.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: id, Channel: channel, Content: &empty, Embeds: &embeds, Components: &components, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(c))
	})
	return discordError(err)
}
func (d *Discord) Delete(ctx context.Context, channel, id string) error {
	_, err := discordCall(d, ctx, func(c context.Context) (struct{}, error) {
		return struct{}{}, d.Session.ChannelMessageDelete(channel, id, discordgo.WithContext(c))
	})
	err = discordError(err)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}
func (d *Discord) Notice(ctx context.Context, channel, content string) error {
	_, err := discordCall(d, ctx, func(c context.Context) (*discordgo.Message, error) {
		return d.Session.ChannelMessageSendComplex(channel, &discordgo.MessageSend{Content: clip(content, 2000), AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(c))
	})
	return discordError(err)
}
