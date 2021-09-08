// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"log"
	"time"

	dotclient "github.com/perun-network/perun-polkadot-backend/client"
	"github.com/spf13/viper"
	"perun.network/go-perun/wire"
)

// Config contains all configuration read from config.yaml and network.yaml
type (
	Config struct {
		Alias   string
		Sk      string
		Channel channelConfig
		Node    nodeConfig
		Chain   dotclient.ChainConfig
		// Read from the network.yaml. The key is the alias.
		Peers map[string]*netConfigEntry
	}

	channelConfig struct {
		Timeout              time.Duration
		FundTimeout          time.Duration
		SettleTimeout        time.Duration
		ChallengeDurationSec uint64
	}

	nodeConfig struct {
		IP               string
		Port             uint16
		DialTimeout      time.Duration
		HandleTimeout    time.Duration
		ReconnectTimeout time.Duration

		PersistencePath    string
		PersistenceEnabled bool
	}

	netConfigEntry struct {
		PerunID  string
		perunID  wire.Address
		Hostname string
		Port     uint16
	}
)

var config Config

// GetConfig returns a pointer to the current `Config`.
// This is needed to make viper and cobra work together.
func GetConfig() *Config {
	return &config
}

// SetConfig called by viper when the config file was parsed
func SetConfig(cfgPath, cfgNetPath string) {
	ParseConfig(cfgPath, cfgNetPath, &config)
}

func ParseConfig(cfgPath, cfgNetPath string, cfg *Config) {
	// Load config files
	viper.SetConfigFile(cfgPath)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	viper.SetConfigFile(cfgNetPath)
	if err := viper.MergeInConfig(); err != nil {
		log.Fatalf("Error reading network config file, %s", err)
	}

	if err := viper.Unmarshal(cfg); err != nil {
		log.Fatal(err)
	}

	for _, peer := range config.Peers {
		addr, err := strToAddress(peer.PerunID)
		if err != nil {
			log.Panic(err)
		}
		peer.perunID = addr
	}
}
