package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Fetcher interface {
	Fetch(context.Context) (Snapshot, error)
}
type channelCards struct {
	Dashboard       string
	Live            map[string]string
	Ready           bool
	Reconciled      time.Time
	Rendered        map[string]string
	NeedsValidation bool
	Cursor          string
	Blocked         map[string]bool
}
type Health struct {
	LastSuccess, FailedSince, LastNotice time.Time
	Failures                             int
}

func (h *Health) Record(now time.Time, err error, t Text) string {
	if err == nil {
		notice := ""
		if h.Failures > 0 {
			notice = fmt.Sprintf(t.Recovered, int(now.Sub(h.FailedSince).Seconds()), h.Failures)
		}
		*h = Health{LastSuccess: now}
		return notice
	}
	h.Failures++
	if h.Failures == 1 {
		h.FailedSince = now
	}
	if h.Failures == 1 || now.Sub(h.LastNotice) >= 15*time.Minute {
		h.LastNotice = now
		return fmt.Sprintf(t.Failure, h.Failures, err.Error())
	}
	return ""
}

type Notifier struct {
	Config   Config
	Render   Renderer
	API      Fetcher
	Discord  Messenger
	Self     string
	States   map[string]*LiveState
	Channels map[string]*channelCards
	Health   Health
	Now      func() time.Time
	Starting bool
}

func newNotifier(c Config, t Text, api Fetcher, d Messenger, self string) *Notifier {
	n := &Notifier{Config: c, Render: Renderer{c, t}, API: api, Discord: d, Self: self, States: map[string]*LiveState{}, Channels: map[string]*channelCards{}, Now: time.Now, Starting: true}
	for _, channel := range c.Channels {
		n.Channels[channel] = &channelCards{Live: map[string]string{}, Rendered: map[string]string{}}
	}
	return n
}
func (n *Notifier) reconcile(ctx context.Context, channel string, c *channelCards, now time.Time) error {
	history, err := n.Discord.History(ctx, channel, c.Cursor, n.Self)
	if err != nil {
		return err
	}
	sort.Slice(history.Messages, func(i, j int) bool { return newerID(history.Messages[j].ID, history.Messages[i].ID) })
	dashboard := c.Dashboard
	live := map[string]string{}
	for id, message := range c.Live {
		live[id] = message
	}
	c.Blocked = map[string]bool{}
	var issues []error
	remove := func(message, key string) {
		if err := n.Discord.Delete(ctx, channel, message); err != nil {
			issues = append(issues, err)
			if key != "" {
				c.Blocked[key] = true
			}
		}
	}
	for _, m := range history.Messages {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		kind, id := n.Render.classify(m, n.Self)
		switch kind {
		case "dashboard":
			if dashboard == "" {
				dashboard = m.ID
			} else if dashboard != m.ID {
				if newerID(dashboard, m.ID) {
					remove(dashboard, "")
					dashboard = m.ID
				} else {
					remove(m.ID, "")
				}
			}
		case "live":
			previous := live[id]
			if previous == "" {
				live[id] = m.ID
			} else if previous != m.ID {
				if newerID(previous, m.ID) {
					remove(previous, identityKey(id))
					live[id] = m.ID
				} else {
					remove(m.ID, identityKey(id))
				}
			}
		}
	}
	c.Dashboard = dashboard
	c.Live = live
	c.Ready = true
	c.Rendered = map[string]string{}
	if len(issues) == 0 {
		c.Cursor = history.Cursor
		c.Reconciled = now
	} else {
		c.Reconciled = time.Time{}
	}
	return errors.Join(issues...)
}
func (n *Notifier) put(ctx context.Context, channel string, c *channelCards, id string, e *discordgo.MessageEmbed) (string, error) {
	if id != "" {
		err := n.Discord.Edit(ctx, channel, id, e)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, errNotFound) {
			return id, err
		}
		// A missing dashboard may mean other cards were deleted too. Recheck them
		// even when minute-only stats have not changed since the last render.
		c.Ready = false
		c.Rendered = map[string]string{}
	}
	m, err := n.Discord.Send(ctx, channel, e)
	if err != nil {
		// A lost response may still have created a message: recover history before retrying.
		c.Ready = false
		return "", err
	}
	return m.ID, nil
}
func (n *Notifier) Step(ctx context.Context) {
	snapshot, fetchErr := n.API.Fetch(ctx)
	for key := range snapshot.Streams {
		if n.Render.streamURL(key) == "" {
			delete(snapshot.Streams, key)
			snapshot.Complete = false
			fetchErr = errors.New("a stream watch URL exceeds Discord's URL length limit")
		}
	}
	now := n.Now()
	if notice := n.Health.Record(now, fetchErr, n.Render.Text); notice != "" {
		slog.Info(notice)
		if n.Config.LogChannel != "" {
			if err := n.Discord.Notice(ctx, n.Config.LogChannel, notice); err != nil {
				slog.Error("Could not send health notice", "error", err)
			}
		}
	}
	for key, stream := range snapshot.Streams {
		state := n.States[key]
		if state == nil || !state.Stream.Start.Equal(stream.Start) {
			state = &LiveState{Pending: n.Starting}
			n.States[key] = state
		}
		state.Update(stream)
	}
	if snapshot.Complete {
		for key := range n.States {
			if _, exists := snapshot.Streams[key]; !exists {
				delete(n.States, key)
			}
		}
	}
	if snapshot.Complete {
		n.Starting = false
	}
	for _, channel := range n.Config.Channels {
		if ctx.Err() != nil {
			return
		}
		if err := n.updateChannel(ctx, channel, snapshot, now); err != nil {
			slog.Error("Could not update notification channel", "channel", channel, "error", err)
		}
	}
}
func (n *Notifier) updateChannel(ctx context.Context, channel string, snapshot Snapshot, now time.Time) error {
	complete := snapshot.Complete
	c := n.Channels[channel]
	var issues []error
	if !c.Ready || c.Reconciled.IsZero() || (complete && c.NeedsValidation) || now.Sub(c.Reconciled) >= 5*time.Minute {
		c.Ready = false
		if err := n.reconcile(ctx, channel, c, now); err != nil {
			issues = append(issues, fmt.Errorf("history recovery: %w", err))
			if !c.Ready {
				return errors.Join(issues...)
			}
		}
	}
	c.NeedsValidation = !complete
	desired := map[string]*LiveState{}
	for _, state := range n.States {
		if !state.Ghost {
			desired[n.Render.liveID(state.Stream)] = state
		}
	}
	blocked := map[string]bool{}
	for key := range c.Blocked {
		blocked[key] = true
	}
	if complete {
		for id, message := range c.Live {
			if ctx.Err() != nil {
				return errors.Join(append(issues, ctx.Err())...)
			}
			if desired[id] == nil {
				if err := n.Discord.Delete(ctx, channel, message); err != nil {
					issues = append(issues, fmt.Errorf("delete stale card: %w", err))
					blocked[identityKey(id)] = true
					continue
				}
				delete(c.Live, id)
				delete(c.Rendered, id)
			}
		}
	}
	var err error
	c.Dashboard, err = n.put(ctx, channel, c, c.Dashboard, n.Render.Dashboard(n.States, now, n.Health.LastSuccess, complete))
	if err != nil {
		issues = append(issues, fmt.Errorf("dashboard: %w", err))
	}
	ids := make([]string, 0, len(desired))
	for id := range desired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			return errors.Join(append(issues, ctx.Err())...)
		}
		state := desired[id]
		if state.Pending || blocked[state.Stream.Key] {
			continue
		}
		if _, fresh := snapshot.Streams[state.Stream.Key]; !fresh {
			continue
		}
		embed := n.Render.Live(state.Stream, now)
		encoded, _ := json.Marshal(embed)
		if c.Live[id] != "" && c.Rendered[id] == string(encoded) {
			continue
		}
		message, err := n.put(ctx, channel, c, c.Live[id], embed)
		if err != nil {
			issues = append(issues, fmt.Errorf("live card: %w", err))
			continue
		}
		c.Live[id] = message
		c.Rendered[id] = string(encoded)
	}
	return errors.Join(issues...)
}
func (n *Notifier) Run(ctx context.Context) {
	for ctx.Err() == nil {
		n.Step(ctx)
		timer := time.NewTimer(n.Config.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
