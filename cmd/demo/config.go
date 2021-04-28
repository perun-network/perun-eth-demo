// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
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
		Chain        chainConfig
		Contracts    contractsConfig
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
		IP              string
		Port            uint16
		DialTimeout     time.Duration
		HandleTimeout   time.Duration
		ReconnecTimeout time.Duration

		PersistencePath    string
		PersistenceEnabled bool
	}

	chainConfig struct {
		TxTimeout time.Duration // timeout duration for on-chain transactions
		URL       string        // URL the endpoint of your ethereum node / infura eg: ws://10.70.5.70:8546
		ID        int64         // Chain ID
	}

	contractsConfig struct {
		Deployment deploymentOption

		Adjudicator common.Address
		Assets      map[string]*assetConfig
	}

	assetConfig struct {
		Type        string         `mapstructure:"type"`
		Assetholder common.Address `mapstructure:"assetholder"`
		Address     common.Address `mapstructure:"address"`
	}

	deploymentOption int
)

var contractSetupOptions = [...]string{"validate", "deploy"}

const (
	contractSetupOptionValidate deploymentOption = iota
	contractSetupOptionDeploy
)

func (option deploymentOption) String() string {
	return contractSetupOptions[option]
}

func parseContractSetupOption(s string) (option deploymentOption, err error) {
	for i, optionString := range contractSetupOptions {
		if s == optionString {
			option = deploymentOption(i)
			return
		}
	}

	err = errors.New(fmt.Sprintf("Invalid value for config option 'contractsetup'. The value is '%s', but must be one of '%v'.", s, contractSetupOptions))
	return
}

type netConfigEntry struct {
	PerunID  string
	perunID  wire.Address
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

	for _, peer := range config.Peers {
		addr, err := strToAddress(peer.PerunID)
		if err != nil {
			log.Panic(err)
		}
		peer.perunID = addr
	}
}

func parseEthAddress() mapstructure.DecodeHookFunc {
	return func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
		// filter by *->common.Address
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
		case reflect.TypeOf(deploymentOption(0)):
			raw, _ := data.(string)
			return parseContractSetupOption(raw)
		default:
			return data, nil
		}
	}
}

func stringToAddress(s string) (common.Address, error) {
	var err error
	var walletAddr wallet.Address

	if walletAddr, err = strToAddress(s); err != nil {
		return common.Address{}, err
	}

	return ewallet.AsEthAddr(walletAddr), nil
}
