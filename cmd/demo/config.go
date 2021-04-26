// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"fmt"
	"log"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
)

// Config contains all configuration read from config.yaml and network.yaml
type Config struct {
	Alias        string
	SecretKey    string
	Mnemonic     string
	AccountIndex uint
	Channel      channelConfig
	Node         nodeConfig
	Chain        chainConfig
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
	IP              string
	Port            uint16
	DialTimeout     time.Duration
	HandleTimeout   time.Duration
	ReconnecTimeout time.Duration

	PersistencePath    string
	PersistenceEnabled bool
}

type contractSetupOption int

var contractSetupOptions [3]string = [...]string{"validate", "deploy", "validateordeploy"}

const (
	contractSetupOptionValidate contractSetupOption = iota
	contractSetupOptionDeploy
	contractSetupOptionValidateOrDeploy
)

func (option contractSetupOption) String() string {
	return contractSetupOptions[option]
}

func parseContractSetupOption(s string) (option contractSetupOption, err error) {
	for i, optionString := range contractSetupOptions {
		if s == optionString {
			option = contractSetupOption(i)
			return
		}
	}

	err = errors.New(fmt.Sprintf("Invalid value for config option 'contractsetup'. The value is '%s', but must be one of '%v'.", s, contractSetupOptions))
	return
}

type chainConfig struct {
	TxTimeout     time.Duration       // timeout duration for on-chain transactions
	ContractSetup string              // contract setup method
	contractSetup contractSetupOption //
	Adjudicator   string              // address of adjudicator contract
	adjudicator   common.Address      //
	Assetholder   string              // address of asset holder contract
	assetholder   common.Address      //
	URL           string              // URL the endpoint of your ethereum node / infura eg: ws://10.70.5.70:8546
	ID            int64               // Chain ID
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

	if err := viper.Unmarshal(&config); err != nil {
		log.Fatal(err)
	}

	var err error
	if config.Chain.contractSetup, err = parseContractSetupOption(config.Chain.ContractSetup); err != nil {
		log.Fatal(err)
	}

	if len(config.Chain.Adjudicator) > 0 {
		if config.Chain.adjudicator, err = stringToAddress(config.Chain.Adjudicator); err != nil {
			log.Fatal(err)
		}
	}

	if len(config.Chain.Assetholder) > 0 {
		if config.Chain.assetholder, err = stringToAddress(config.Chain.Assetholder); err != nil {
			log.Fatal(err)
		}
	}

	for _, peer := range config.Peers {
		addr, err := strToAddress(peer.PerunID)
		if err != nil {
			log.Panic(err)
		}
		peer.perunID = addr
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
