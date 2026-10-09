// Package config loads non-secret money policy (pricing bands, fee policy)
// from config.yaml. Secrets must NEVER live here — they stay in env.
//
// Resolution order: CONFIG_PATH env → ./config.yaml → ./backend/config.yaml
// → <exedir>/config.yaml. With no file found, compiled defaults (documented
// below, matching launch policy) apply with a warning — but an explicit
// CONFIG_PATH pointing nowhere is a hard error. Invalid values fail fast at
// boot; a bad rate must never reach the ledger.
package config

import (
	"fmt"
	"os"

	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

// Band maps a minimum risk score to a nominal APR (% per annum).
type Band struct {
	MinScore int             `yaml:"minScore"`
	Rate     decimal.Decimal `yaml:"rate"`
}

// Pricing is an ordered (descending MinScore) band table. The last band must
// cover the score floor so every borrower prices.
type Pricing struct {
	Bands []Band `yaml:"bands"`
}

// RateFor returns the nominal APR for a risk score. Bands are first match.
func (p Pricing) RateFor(score int) decimal.Decimal {
	for _, b := range p.Bands {
		if score >= b.MinScore {
			return b.Rate
		}
	}
	return decimal.Zero
}

// Fees holds the late-fee policy: a one-time fraction of the installment,
// capped per installment. FeeRate is a fraction (0.05 = 5%), not percent.
type Fees struct {
	LateFeeRate decimal.Decimal `yaml:"lateFeeRate"`
	LateFeeCap  decimal.Decimal `yaml:"lateFeeCap"`
}

type Config struct {
	Pricing Pricing `yaml:"pricing"`
	Fees    Fees    `yaml:"fees"`
}

// Default returns the launch policy (also the fallback when no file exists).
func Default() Config {
	return Config{
		Pricing: Pricing{Bands: []Band{
			{MinScore: 740, Rate: decimal.NewFromFloat(12.0)},
			{MinScore: 670, Rate: decimal.NewFromFloat(18.0)},
			{MinScore: 580, Rate: decimal.NewFromFloat(24.0)},
			{MinScore: 0, Rate: decimal.NewFromFloat(30.0)},
		}},
		Fees: Fees{
			LateFeeRate: decimal.NewFromFloat(0.05),
			LateFeeCap:  decimal.NewFromInt(5000),
		},
	}
}

// raw mirrors Config with float64s (friendlier YAML than decimal strings).
type rawBand struct {
	MinScore int     `yaml:"minScore"`
	Rate     float64 `yaml:"rate"`
}

type rawConfig struct {
	Pricing struct {
		Bands []rawBand `yaml:"bands"`
	} `yaml:"pricing"`
	Fees struct {
		LateFeeRate float64 `yaml:"lateFeeRate"`
		LateFeeCap  float64 `yaml:"lateFeeCap"`
	} `yaml:"fees"`
}

// Load reads and validates the money-policy file. An empty path means
// CONFIG_PATH, falling back to backend/config.yaml — i.e. run from the repo
// root in dev (the Docker image sets CONFIG_PATH=/srv/config.yaml, systemd
// units should too). One explicit rule, no directory probing: a missing or
// invalid file fails fast. There is intentionally no silent-defaults
// fallback at runtime (Default() exists for tests only) — an absent policy
// file must never quietly become launch policy with real money.
func Load(path string) (cfg Config, source string, err error) {
	if path == "" {
		path = os.Getenv("CONFIG_PATH")
	}
	if path == "" {
		path = "backend/config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, "", fmt.Errorf("read config %s: %w (set CONFIG_PATH)", path, err)
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, "", fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg, err = build(raw, path)
	if err != nil {
		return Config{}, "", err
	}
	return cfg, path, nil
}

func build(raw rawConfig, path string) (Config, error) {
	where := func(what string) error { return fmt.Errorf("config %s: invalid %s", path, what) }
	if len(raw.Pricing.Bands) == 0 {
		return Config{}, where("pricing.bands (at least one band required)")
	}
	cfg := Config{}
	prev := int(^uint(0) >> 1) // max int: bands must descend
	for i, b := range raw.Pricing.Bands {
		if b.Rate < 0 || b.Rate > 100 {
			return Config{}, where(fmt.Sprintf("pricing.bands[%d].rate (0-100)", i))
		}
		if b.MinScore < 0 || b.MinScore > 850 {
			return Config{}, where(fmt.Sprintf("pricing.bands[%d].minScore (0-850)", i))
		}
		if b.MinScore >= prev {
			return Config{}, where(fmt.Sprintf("pricing.bands[%d].minScore (must descend)", i))
		}
		prev = b.MinScore
		cfg.Pricing.Bands = append(cfg.Pricing.Bands, Band{
			MinScore: b.MinScore,
			Rate:     decimal.NewFromFloat(b.Rate),
		})
	}
	if last := cfg.Pricing.Bands[len(cfg.Pricing.Bands)-1]; last.MinScore > 300 {
		return Config{}, where("pricing.bands (last band must cover score floor 300)")
	}
	if raw.Fees.LateFeeRate < 0 || raw.Fees.LateFeeRate > 1 {
		return Config{}, where("fees.lateFeeRate (0-1 fraction)")
	}
	if raw.Fees.LateFeeCap < 0 {
		return Config{}, where("fees.lateFeeCap (>= 0)")
	}
	cfg.Fees = Fees{
		LateFeeRate: decimal.NewFromFloat(raw.Fees.LateFeeRate),
		LateFeeCap:  decimal.NewFromFloat(raw.Fees.LateFeeCap),
	}
	return cfg, nil
}
