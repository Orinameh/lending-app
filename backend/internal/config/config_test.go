package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validYAML = `
pricing:
  bands:
    - minScore: 740
      rate: 12
    - minScore: 0
      rate: 30
fees:
  lateFeeRate: 0.05
  lateFeeCap: 5000
`

func TestLoadValid(t *testing.T) {
	cfg, src, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if src == "" || src == "compiled-defaults" {
		t.Fatalf("want resolved file source, got %q", src)
	}
	if got := cfg.Pricing.RateFor(800).String(); got != "12" {
		t.Fatalf("top band = %s, want 12", got)
	}
	if got := cfg.Pricing.RateFor(100).String(); got != "30" {
		t.Fatalf("bottom band = %s, want 30", got)
	}
	if got := cfg.Fees.LateFeeRate.String(); got != "0.05" {
		t.Fatalf("fee rate = %s, want 0.05", got)
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"rate over 100": `
pricing: {bands: [{minScore: 0, rate: 101}]}
fees: {lateFeeRate: 0.05, lateFeeCap: 1}
`,
		"bands ascending": `
pricing: {bands: [{minScore: 0, rate: 30}, {minScore: 740, rate: 12}]}
fees: {lateFeeRate: 0.05, lateFeeCap: 1}
`,
		"floor uncovered": `
pricing: {bands: [{minScore: 700, rate: 12}]}
fees: {lateFeeRate: 0.05, lateFeeCap: 1}
`,
		"fee fraction over 1": `
pricing: {bands: [{minScore: 0, rate: 30}]}
fees: {lateFeeRate: 1.5, lateFeeCap: 1}
`,
		"negative cap": `
pricing: {bands: [{minScore: 0, rate: 30}]}
fees: {lateFeeRate: 0.05, lateFeeCap: -1}
`,
		"empty bands": `
pricing: {bands: []}
fees: {lateFeeRate: 0.05, lateFeeCap: 1}
`,
	}
	for name, body := range cases {
		if _, _, err := Load(writeTemp(t, body)); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestDefaultsMatchLaunchPolicy(t *testing.T) {
	d := Default()
	if got := d.Pricing.RateFor(760).String(); got != "12" {
		t.Fatalf("default top band = %s", got)
	}
	if got := d.Fees.LateFeeCap.String(); got != "5000" {
		t.Fatalf("default cap = %s", got)
	}
}

func TestExplicitMissingPathErrors(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join(t.TempDir(), "nope.yaml"))
	if _, _, err := Load(""); err == nil {
		t.Fatal("explicit missing CONFIG_PATH must error")
	}
}
