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

type Config struct {
	DefaultInterval  int64    `json:"default_interval"`
	DefaultThreshold int64    `json:"default_threshold"`
	DefaultAlertCmd  []string `json:"default_alert_cmd"`
	Targets          []struct {
		Interval  int64    `json:"interval"`
		Threshold int64    `json:"threshold"`
		AlertCmd  []string `json:"alert_cmd"`
		WatchCmd  []string `json:"watch_cmd"`
	} `json:"targets"`
}

type Runner struct {
	lastSuccess time.Time
	interval    time.Duration
	threshold   time.Duration
	alertCmd    []string
	watchCmd    []string
}

func (r *Runner) Run(ctx context.Context) error {
	r.lastSuccess = time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			now := time.Now()
			cmd := exec.CommandContext(ctx, r.watchCmd[0], r.watchCmd[1:]...)
			if err := cmd.Run(); err == nil {
				r.lastSuccess = now
			}

			if now.Sub(r.lastSuccess) > r.threshold {
				cmd := exec.CommandContext(ctx, r.alertCmd[0], r.alertCmd[1:]...)
				if err := cmd.Run(); err != nil {
					return err
				}
			}
			time.Sleep(r.interval)
		}
	}
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

	g, ctx := errgroup.WithContext(context.Background())

	for i := range cfg.Targets {
		g.Go(func() error {
			var interval, threshold int64
			var alertCmd []string
			if cfg.Targets[i].Interval == 0 {
				interval = cfg.DefaultInterval
			} else {
				interval = cfg.Targets[i].Interval
			}
			if cfg.Targets[i].Threshold == 0 {
				threshold = cfg.DefaultThreshold
			} else {
				threshold = cfg.Targets[i].Threshold
			}
			if len(cfg.Targets[i].AlertCmd) == 0 {
				alertCmd = cfg.DefaultAlertCmd
			} else {
				alertCmd = cfg.Targets[i].AlertCmd
			}
			r := &Runner{
				interval:  time.Duration(interval) * time.Second,
				threshold: time.Duration(threshold) * time.Second,
				alertCmd:  alertCmd,
				watchCmd:  cfg.Targets[i].WatchCmd,
			}
			return r.Run(ctx)
		})
	}

	if err := g.Wait(); err != nil {
		log.Panic(err)
	}
}
