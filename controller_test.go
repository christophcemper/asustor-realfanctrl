package main

import (
	"testing"
	"time"
)

// run feeds one target per 2 s cycle and returns the applied PWM per cycle.
func run(cfg Config, wants []int) []int {
	ctl := &controller{cfg: cfg}
	t0 := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	out := make([]int, len(wants))
	for i, w := range wants {
		out[i] = ctl.step(w, t0.Add(time.Duration(2*i)*time.Second))
	}
	return out
}

func testConfig() Config {
	cfg := defaultConfig()
	cfg.normalize()
	return cfg
}

func TestStepRiseDelay(t *testing.T) {
	cases := []struct {
		name  string
		mod   func(*Config)
		wants []int
		want  []int
	}{
		{
			name:  "short CPU spike is ignored",
			wants: []int{60, 76, 72, 60, 60},
			want:  []int{60, 60, 60, 60, 60},
		},
		{
			name:  "sustained rise applies after the delay at the lowest held target",
			wants: []int{60, 70, 74, 72, 75, 73, 74, 74},
			want:  []int{60, 60, 60, 60, 60, 60, 70, 70},
		},
		{
			name:  "big jump bypasses the delay",
			wants: []int{60, 172, 172},
			want:  []int{60, 172, 172},
		},
		{
			name:  "fall stays rate-limited",
			wants: []int{80, 60, 60, 60},
			want:  []int{80, 78, 76, 74},
		},
		{
			name:  "delay 0 rises immediately",
			mod:   func(c *Config) { c.RiseDelaySec = 0 },
			wants: []int{60, 66, 70},
			want:  []int{60, 66, 70},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			if tc.mod != nil {
				tc.mod(&cfg)
			}
			got := run(cfg, tc.wants)
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("cycle %d: got %v, want %v", i, got, tc.want)
				}
			}
		})
	}
}

func TestOldConfigGetsRiseDefaults(t *testing.T) {
	cfg := defaultConfig()
	cfg.RiseBypass = 0 // as if the key were set to 0 by hand
	cfg.normalize()
	if cfg.RiseDelaySec != 10 || cfg.RiseBypass != 40 {
		t.Fatalf("got delay %d bypass %d, want 10 and 40", cfg.RiseDelaySec, cfg.RiseBypass)
	}
}
