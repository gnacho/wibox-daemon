package main

import (
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	for _, k := range []string{"WIBOX_API_PORT", "WIBOX_CHANNEL", "WIBOX_STREAM", "WIBOX_REMOTE",
		"WIBOX_REMOTE_ONLY", "WIBOX_REMOTE_ATTEMPTS", "WIBOX_AWAKE_POLL", "WIBOX_REMOTE_BUDGET",
		"WIBOX_CLOUD_CHECK", "WIBOX_VIDEO_LOCK", "WIBOX_UNLOCK_PASSWORD"} {
		t.Setenv(k, "")
	}
	cfg := loadConfig()
	if cfg.apiPort != 8087 {
		t.Fatalf("apiPort=%d", cfg.apiPort)
	}
	if cfg.stream != 2 {
		t.Fatalf("stream por defecto debe ser 2, es %d", cfg.stream)
	}
	if cfg.channel != 1 {
		t.Fatalf("channel=%d", cfg.channel)
	}
	if !cfg.remote || cfg.remoteOnly {
		t.Fatalf("remote=%v remoteOnly=%v", cfg.remote, cfg.remoteOnly)
	}
	if cfg.cloudCheck != 600*time.Second {
		t.Fatalf("cloudCheck=%s", cfg.cloudCheck)
	}
	if cfg.pin != "" {
		t.Fatalf("el pin no debe tener valor por defecto: %q", cfg.pin)
	}
}

func TestLoadConfigReadsTheEnvironment(t *testing.T) {
	t.Setenv("WIBOX_API_PORT", "9999")
	t.Setenv("WIBOX_CHANNEL", "3")
	t.Setenv("WIBOX_STREAM", "4")
	t.Setenv("WIBOX_REMOTE", "0")
	t.Setenv("WIBOX_REMOTE_ONLY", "1")
	t.Setenv("WIBOX_REMOTE_ATTEMPTS", "7")
	t.Setenv("WIBOX_AWAKE_POLL", "11")
	t.Setenv("WIBOX_REMOTE_BUDGET", "99")
	t.Setenv("WIBOX_CLOUD_CHECK", "120")
	t.Setenv("WIBOX_VIDEO_LOCK", "/tmp/otro.lock")
	cfg := loadConfig()
	if cfg.apiPort != 9999 || cfg.channel != 3 || cfg.stream != 4 {
		t.Fatalf("puerto/canal/stream = %d/%d/%d", cfg.apiPort, cfg.channel, cfg.stream)
	}
	if cfg.remote || !cfg.remoteOnly {
		t.Fatalf("remote=%v remoteOnly=%v", cfg.remote, cfg.remoteOnly)
	}
	if cfg.remoteAttempts != 7 {
		t.Fatalf("remoteAttempts=%d", cfg.remoteAttempts)
	}
	if cfg.remotePoll != 11*time.Second {
		t.Fatalf("remotePoll=%s", cfg.remotePoll)
	}
	if cfg.remoteBudget != 99*time.Second {
		t.Fatalf("remoteBudget=%s", cfg.remoteBudget)
	}
	if cfg.cloudCheck != 120*time.Second {
		t.Fatalf("cloudCheck=%s", cfg.cloudCheck)
	}
	if cfg.videoLock != "/tmp/otro.lock" {
		t.Fatalf("videoLock=%q", cfg.videoLock)
	}
}
