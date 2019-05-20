package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sync/errgroup"
)

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	td, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(td)
	return nil
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

type Config struct {
	DefaultInterval  Duration `json:"default_interval"`
	DefaultThreshold Duration `json:"default_threshold"`
	DefaultAlertCmd  []string `json:"default_alert_cmd"`
	DefaultTimeout   Duration `json:"default_timeout"`
	Targets          []struct {
		Name      string   `json:"name"`
		Interval  Duration `json:"interval"`
		Threshold Duration `json:"threshold"`
		Timeout   Duration `json:"timeout"`
		AlertCmd  []string `json:"alert_cmd"`
		NoDataCmd []string `json:"no_data_cmd"`
		WatchCmd  []string `json:"watch_cmd"`
	} `json:"targets"`
}

func (c *Config) FillWithDefaults() {
	for i := range c.Targets {
		if c.Targets[i].Interval == 0 {
			c.Targets[i].Interval = c.DefaultInterval
		}
		if c.Targets[i].Threshold == 0 {
			c.Targets[i].Threshold = c.DefaultThreshold
		}
		if len(c.Targets[i].AlertCmd) == 0 {
			c.Targets[i].AlertCmd = c.DefaultAlertCmd
		}
		if c.Targets[i].Timeout == 0 {
			c.Targets[i].Timeout = c.DefaultTimeout
		}
	}
}

type Target struct {
	Name        string
	LastSuccess time.Time
	LastData    time.Time
	Threshold   time.Duration
	AlertCmd    []string
	NoDataCmd   []string
}

type Result struct {
	Name      string
	Succeeded bool
	Timestamp time.Time
}

type Server struct {
	Targets map[string]*Target
	ResCh   chan Result
}

func (s *Server) Register(t *Target) {
	if s.Targets == nil {
		s.Targets = make(map[string]*Target)
	}
	s.Targets[t.Name] = t
}

func (r *Server) Run(ctx context.Context) error {
	for res := range r.ResCh {
		now := time.Now()
		tgt := r.Targets[res.Name]
		if tgt.LastSuccess.After(res.Timestamp) {
			tgt.LastData = res.Timestamp
			if res.Succeeded {
				tgt.LastSuccess = res.Timestamp
			}
		}
		if tgt.NoDataCmd != nil && now.Sub(tgt.LastData) > tgt.Threshold {
			go func(cmd []string) {
				exec.Command(cmd[0], cmd[1:]...).Run()
			}(tgt.NoDataCmd)
			continue
		}
		if tgt.AlertCmd != nil && now.Sub(tgt.LastSuccess) > tgt.Threshold {
			go func(cmd []string) {
				exec.Command(cmd[0], cmd[1:]...).Run()
			}(tgt.AlertCmd)
		}
	}
	return nil
}

func (r *Server) NoDataWatch(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			for _, t := range r.Targets {
				r.ResCh <- Result{Name: t.Name}
			}
		}
	}
	return nil
}

type Watcher struct {
	Name     string
	ResCh    chan<- Result
	Interval time.Duration
	Timeout  time.Duration
	WatchCmd []string
}

func (r *Watcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			go func() {
				ctx, cancel := context.WithTimeout(ctx, r.Timeout)
				defer cancel()
				now := time.Now()
				err := exec.CommandContext(ctx, r.WatchCmd[0], r.WatchCmd[1:]...).Run()
				r.ResCh <- Result{
					Name:      r.Name,
					Succeeded: err == nil,
					Timestamp: now,
				}
			}()
		}
	}
	return nil
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Panic(err)
	}
	cfgf := filepath.Join(home, ".config", "locdog", "config.json")
	f, err := os.Open(cfgf)
	if err != nil {
		log.Panic(err)
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		log.Panic(err)
	}
	cfg.FillWithDefaults()

	g, ctx := errgroup.WithContext(context.Background())

	s := new(Server)
	ResCh := make(chan Result)
	s.ResCh = ResCh
	for _, t := range cfg.Targets {
		s.Register(&Target{
			Name:      t.Name,
			Threshold: t.Threshold.Value(),
			AlertCmd:  t.AlertCmd,
			NoDataCmd: t.NoDataCmd,
		})
		g.Go(func() error {
			w := &Watcher{
				Name:     t.Name,
				Interval: t.Interval.Value(),
				Timeout:  t.Timeout.Value(),
				WatchCmd: t.WatchCmd,
				ResCh:    ResCh,
			}
			return w.Run(ctx)
		})
	}
	g.Go(func() error {
		return s.NoDataWatch(ctx)
	})

	if err := g.Wait(); err != nil {
		log.Panic(err)
	}
}
