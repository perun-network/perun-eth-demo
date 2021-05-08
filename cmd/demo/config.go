// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
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
		GasLimit  uint64
		GasPrice  int64
	}

	contractConfig struct {
		Deployment deploymentOption

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

			if strings.HasPrefix(addr, "$") {
				contractName := strings.TrimPrefix(addr, "$")
				contractNameJSON, ok := contractNameRegistry[contractName]
				if !ok {
					return nil, errors.Errorf("unknown contract name %s", contractName)
				}

				deployedAddr, err := getDeployedAddress(contractNameJSON)
				if err != nil {
					return nil, err
				}
				addr = *deployedAddr
			}

			if len(addr) != 42 {
				return nil, errors.New("ethereum address must be 42 characters long")
			}
			if !common.IsHexAddress(addr) {
				return nil, errors.New("invalid ethereum address")
			}
			return common.HexToAddress(addr), nil
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

// getDeployedAddress returns the contract's address with name `contractName`
// that should be saved in a JSON file in the ../perun-eth-contracts folder.
func getDeployedAddress(contractName string) (*string, error) {
	contractAddrsFile, err := os.Open("../perun-eth-contracts/deployed-contracts.json")
	if err != nil {
		return nil, errors.Wrap(err, "opening contracts addresses file")
	}
	defer contractAddrsFile.Close()

	var contractAddrs map[string]string
	byteValue, _ := ioutil.ReadAll(contractAddrsFile)
	err = json.Unmarshal(byteValue, &contractAddrs)
	if err != nil {
		return nil, errors.Wrap(err, "parsing contract addresses file")
	}
	addr, ok := contractAddrs[contractName]

	if !ok {
		return nil, errors.Errorf("contract %s not found in contract addresses file", contractName)
	}
	return &addr, nil
}

func (c Config) findAsset(addr common.Address) (*asset, bool) {
	for _, asset := range c.Contracts.Assets {
		if bytes.Equal(asset.Assetholder.Bytes(), addr.Bytes()) {
			return asset, true
		}
	}
	return nil, false
}
