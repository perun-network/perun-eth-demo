// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"bytes"
	"reflect"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"perun.network/go-perun/log"
)

// Config contains all configuration read from config.yaml and network.yaml
type (
	Config struct {
		Alias        string
		SecretKey    string
		Mnemonic     string
		AccountIndex uint
		Channel      channelConfig
		Node         nodeConfig
		Chain        chainConfig             // The selected chain config.
		Chains       map[string]*chainConfig // All configured chain configs.
		Contracts    contractConfig
		// Read from the network.yaml. The key is the alias.
		Peers map[string]*netConfigEntry
		Hub   hubConfig
	}

	channelConfig struct {
		Timeout              time.Duration
		FundTimeout          time.Duration
		SettleTimeout        time.Duration
		ChallengeDurationSec uint64
	}

	nodeConfig struct {
		IP              string
		Port            uint16
		DialTimeout     time.Duration
		HandleTimeout   time.Duration
		ReconnecTimeout time.Duration

		PersistencePath    string
		PersistenceEnabled bool
	}

	hubConfig struct {
		Side hubSide
		IP   string
		Port uint16
	}

	chainConfig struct {
		TxTimeout time.Duration // timeout duration for on-chain transactions
		URL       string        // URL the endpoint of your ethereum node / infura eg: ws://10.70.5.70:8546
		ID        int64         // Chain ID
		GasLimit  uint64
		GasPrice  int64
	}

	contractConfig struct {
		Deployment deploymentOption

		contractAddresses `mapstructure:",squash"`
	}

	contractAddresses struct {
		Adjudicator common.Address
		Assets      map[string]*asset
	}

	asset struct {
		Type        assetType      `mapstructure:"type"`
		Address     common.Address `mapstructure:"address"`
		Assetholder common.Address `mapstructure:"assetholder"`
	}

	netConfigEntry struct {
		PerunID  common.Address
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
func SetConfig() {
	// Load config files
	viper.SetConfigFile(flags.cfgFile)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	viper.SetConfigFile(flags.cfgNetFile)
	if err := viper.MergeInConfig(); err != nil {
		log.Fatalf("Error reading network config file, %s", err)
	}

	opts := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		parseEthAddress(),
		mapstructure.StringToTimeDurationHookFunc(), // default
		mapstructure.StringToSliceHookFunc(","),     // default
	))
	if err := viper.Unmarshal(&config, opts); err != nil {
		log.Fatal(err)
	}

	// If a contract addresses file is provided, use those addresses instead.
	if flags.cfgCtrFile != "" {
		v := viper.New()
		v.SetConfigFile(flags.cfgCtrFile)
		if err := v.ReadInConfig(); err != nil {
			log.Fatalf("Error reading contracts config file, %s", err)
		}

		var ctrAddresses contractAddresses
		if err := v.Unmarshal(&ctrAddresses, opts); err != nil {
			log.Fatal(err)
		}
		// Override contract addresses.
		config.Contracts.contractAddresses = ctrAddresses
	}

	chain, ok := config.Chains[flags.chain]
	if !ok {
		log.Fatalf("Could not find chain config '%s'", flags.chain)
	}
	log.Infof("Using chain '%s'", flags.chain)
	config.Chain = *chain
}

// parseEthAddress is used by viper to parse the custom types out of the yaml struct.
func parseEthAddress() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
		switch to {
		case reflect.TypeOf(common.Address{}):
			addr, ok := data.(string)
			if !ok {
				return nil, errors.New("expected a string for an address")
			}

			if len(addr) != 42 {
				return nil, errors.New("ethereum address must be 42 characters long")
			}
			if !common.IsHexAddress(addr) {
				return nil, errors.New("invalid ethereum address")
			}
			return common.HexToAddress(addr), nil
		case reflect.TypeOf(hubSide(0)):
			return parseHubSide(data.(string))
		case reflect.TypeOf(deploymentOption(0)):
			return parseContractSetupOption(data.(string))
		case reflect.TypeOf(assetType(0)):
			return parseAssetType(data.(string))
		default:
			return data, nil
		}
	}
}

func (c Config) peerAddresses() []common.Address {
	addrs := make([]common.Address, 0)
	for _, peer := range c.Peers {
		addrs = append(addrs, peer.PerunID)
	}
	return addrs
}

func (c Config) findAsset(addr common.Address) (*asset, bool) {
	for _, asset := range c.Contracts.Assets {
		if bytes.Equal(asset.Assetholder.Bytes(), addr.Bytes()) {
			return asset, true
		}
	}
	return nil, false
}
