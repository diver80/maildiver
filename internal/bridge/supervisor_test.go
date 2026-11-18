package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/diver80/omathunder/internal/config"
)

type switchableConfig struct {
	mu  sync.Mutex
	cfg config.Config
	err error
}

func (c *switchableConfig) load() (config.Config, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg, c.err
}

func (c *switchableConfig) set(fn func(*config.Config)) {
	c.mu.Lock()
	fn(&c.cfg)
	c.mu.Unlock()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSupervisorFollowsBridgeEnabled(t *testing.T) {
	disc := filepath.Join(t.TempDir(), "bridge.json")
	sc := &switchableConfig{cfg: config.Default()}
	if sc.cfg.Bridge.Enabled {
		t.Fatal("bridge must be opt-in by default")
	}

	sup := StartSupervisor(context.Background(), Options{
		DiscoveryPath: disc,
		RPC:           &fakeRPC{connected: true},
		ConfigLoader:  sc.load,
	}, 20*time.Millisecond, nil)
	defer sup.Close()

	if sup.Current() != nil {
		t.Fatal("bridge must not start while disabled")
	}
	if _, err := os.Stat(disc); !os.IsNotExist(err) {
		t.Fatalf("no discovery file expected while disabled, err=%v", err)
	}

	sc.set(func(c *config.Config) { c.Bridge.Enabled = true })
	sup.Reconcile()
	waitFor(t, "bridge start", func() bool { return sup.Current() != nil })
	if _, err := os.Stat(disc); err != nil {
		t.Fatalf("discovery file missing after enable: %v", err)
	}
	first := sup.Current()

	// An unreadable config keeps the running bridge.
	sc.set(func(c *config.Config) {})
	sc.mu.Lock()
	sc.err = errors.New("broken json")
	sc.cfg = config.Default() // what config.Load returns on parse errors
	sc.mu.Unlock()
	time.Sleep(80 * time.Millisecond)
	if sup.Current() != first {
		t.Fatal("unreadable config must not stop the bridge")
	}
	sc.mu.Lock()
	sc.err = nil
	sc.cfg.Bridge.Enabled = true
	sc.mu.Unlock()

	sc.set(func(c *config.Config) { c.Bridge.Enabled = false })
	waitFor(t, "bridge stop", func() bool { return sup.Current() == nil })
	waitFor(t, "discovery cleanup", func() bool {
		_, err := os.Stat(disc)
		return os.IsNotExist(err)
	})
}

func TestCloseKeepsForeignDiscoveryFile(t *testing.T) {
	disc := filepath.Join(t.TempDir(), "bridge.json")
	cfg := config.Default()
	srv, err := Start(context.Background(), Options{
		DiscoveryPath: disc,
		RPC:           &fakeRPC{connected: true},
		ConfigLoader:  func() (config.Config, error) { return cfg, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Another host (e.g. a second Thunderbird profile) took over the discovery file.
	foreign := srv.Info()
	foreign.PID++
	foreign.Token = "other"
	if err := writeDiscoveryFile(disc, foreign); err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDiscoveryFile(disc)
	if err != nil || got.Token != "other" {
		t.Fatalf("foreign discovery file must survive Close, got %+v err=%v", got, err)
	}
}
