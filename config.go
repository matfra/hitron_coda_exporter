package main

import (
	"io"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

type config struct {
	Host               string `yaml:"host"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	Scheme             string `yaml:"scheme"`
	ModemType          string `yaml:"modem_type"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	CollectRouter      *bool  `yaml:"collect_router"`
	CollectWiFi        *bool  `yaml:"collect_wifi"`
}

// parse a config file
func parse(in io.Reader) (*config, error) {
	out := &config{}
	dec := yaml.NewDecoder(in)

	err := dec.Decode(out)
	if err != nil && err != io.EOF {
		return out, err
	}

	return out, nil
}

type safeConfig struct {
	C *config
	sync.RWMutex
}

func (sc *safeConfig) ReloadConfig(configFile string) (err error) {
	f, err := os.Open(configFile)
	if err != nil {
		return err
	}

	conf, err := parse(f)
	if err != nil {
		return err
	}

	sc.Lock()
	sc.C = conf
	sc.Unlock()

	return nil
}
