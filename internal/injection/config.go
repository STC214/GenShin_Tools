package injection

import (
	"errors"
	"strings"
)

type Config struct {
	Enabled          bool   `json:"enabled"`
	RiskAcknowledged bool   `json:"riskAcknowledged"`
	ElevatedHelper   bool   `json:"elevatedHelper"`
	ModuleID         string `json:"moduleId"`
	HelperTimeoutMS  int    `json:"helperTimeoutMs"`
	RemoteTimeoutMS  int    `json:"remoteTimeoutMs"`
}

func DefaultConfig() Config {
	return Config{ElevatedHelper: true, HelperTimeoutMS: 15_000, RemoteTimeoutMS: 5_000}
}

func (config Config) Normalized() (Config, error) {
	config.ModuleID = strings.TrimSpace(config.ModuleID)
	if config.ModuleID != "" && !moduleIDPattern.MatchString(config.ModuleID) {
		return Config{}, errors.New("injection module id is invalid")
	}
	if config.HelperTimeoutMS < 3000 || config.HelperTimeoutMS > 60_000 {
		return Config{}, errors.New("injection helper timeout must be within 3000..60000 ms")
	}
	if config.RemoteTimeoutMS < 1000 || config.RemoteTimeoutMS > 30_000 || config.RemoteTimeoutMS >= config.HelperTimeoutMS {
		return Config{}, errors.New("remote timeout must be within 1000..30000 ms and shorter than helper timeout")
	}
	return config, nil
}
