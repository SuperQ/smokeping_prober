// Copyright 2021 Ben Kochie <superq@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"fmt"
	"os"
	"sync"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "smokeping_prober"

var (
	configReloadSuccess = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "config_last_reload_successful",
		Help:      "Blackbox exporter config loaded successfully.",
	})

	configReloadSeconds = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "config_last_reload_success_timestamp_seconds",
		Help:      "Timestamp of the last successful configuration reload.",
	})

	// DefaultTargetGroup sets the default configuration for the TargetGroup
	DefaultTargetGroup = TargetGroup{
		Interval: time.Second,
		Network:  "ip",
		Protocol: "icmp",
	}
)

type Config struct {
	Targets []TargetGroup `yaml:"targets"`
}

type SafeConfig struct {
	sync.RWMutex
	C *Config
}

func (sc *SafeConfig) ReloadConfig(confFile string) (err error) {
	var c = &Config{}
	defer func() {
		if err != nil {
			configReloadSuccess.Set(0)
		} else {
			configReloadSuccess.Set(1)
			configReloadSeconds.SetToCurrentTime()
		}
	}()

	yamlReader, err := os.Open(confFile)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}
	defer yamlReader.Close()
	decoder := yaml.NewDecoder(yamlReader)
	decoder.KnownFields(true)

	if err = decoder.Decode(c); err != nil {
		return fmt.Errorf("error parsing config file: %w", err)
	}

	sc.Lock()
	sc.C = c
	sc.Unlock()

	return nil
}

type TargetGroup struct {
	Hosts    []string      `yaml:"hosts"`
	Interval time.Duration `yaml:"interval,omitempty"`
	Network  string        `yaml:"network,omitempty"`
	Protocol string        `yaml:"protocol,omitempty"`
	// TODO: Needs work to fix MetricFamily consistency.
	// Labels   map[string]string `yaml:"labels,omitempty"`
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (s *Config) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type plain Config
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}
	return nil
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (s *TargetGroup) UnmarshalYAML(unmarshal func(interface{}) error) error {
	*s = DefaultTargetGroup
	type plain TargetGroup
	if err := unmarshal((*plain)(s)); err != nil {
		return err
	}
	return nil
}
