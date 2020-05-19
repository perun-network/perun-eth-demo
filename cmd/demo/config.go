// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"log"
	"time"

	"github.com/spf13/viper"
	perun "perun.network/go-perun/peer"
)

// Config contains all configuration read from config.yaml and network.yaml
type Config struct {
	Alias      string
	SecretKey  string
	WalletPath string
	Channel    channelConfig
	Node       nodeConfig
	Chain      chainConfig
	// Read from the network.yaml. The key is the alias.
	Peers map[string]*netConfigEntry
}

type channelConfig struct {
	Timeout              time.Duration
	FundTimeout          time.Duration
	SettleTimeout        time.Duration
	ChallengeDurationSec uint64
}

type nodeConfig struct {
	IP            string
	Port          uint16
	DialTimeout   time.Duration
	HandleTimeout time.Duration
}

type chainConfig struct {
	TxTimeout time.Duration

	Adjudicator string
	Assetholder string
	// URL the endpoint of your ethereum node / infura eg: ws://10.70.5.70:8546
	URL string
}

type netConfigEntry struct {
	PerunID  string
	perunID  perun.Address
	Lel      string
	Hostname string
	Port     uint16
}

var config Config

// GetConfig returns a pointer to the current `Config`.
// This is needed to make viper and cobra work together.
func GetConfig() *Config {
	return &config
}

// SetConfig called by viper when the config file was parsed
func SetConfig() {
	if err := viper.Unmarshal(&config); err != nil {
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
