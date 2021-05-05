// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo // import "github.com/perun-network/perun-eth-demo/cmd/demo"

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
)

type (
	assetName        string
	assetType        int
	deploymentOption int
	hubSide          int
)

var contractSetupOptions = [...]string{"validate", "deploy", "none"}
var assetTypeOptions = [...]string{"eth", "erc20"}
var assetTypeSymbols = [...]string{"Ξ", "PRN"}
var hubSides = [...]string{"", "active", "passive"}

var contractNameRegistry = map[string]string{
	"adjudicator_address":      "adjudicator",
	"assetholderETH_address":   "assetholderETH",
	"assetholderERC20_address": "assetholderERC20",
	"peruntoken_address":       "perunToken",
}

const (
	contractSetupOptionValidate deploymentOption = iota
	contractSetupOptionDeploy
	contractSetupOptionNone
)

const (
	assetNameEth   assetName = "eth"
	assetNameERC20 assetName = "peruntoken"
)

const (
	assetTypeEth assetType = iota
	assetTypeErc20
)

const (
	hubSideDisabled hubSide = iota // default
	hubSideActive
	hubSidePassive
)

func (option deploymentOption) String() string {
	return contractSetupOptions[option]
}

func (option assetType) String() string {
	return assetTypeOptions[option]
}

func (option assetType) Symbol() string {
	return assetTypeSymbols[option]
}

func (side hubSide) String() string {
	return hubSides[side]
}

func parseContractSetupOption(s string) (option deploymentOption, err error) {
	s = strings.ToLower(s)
	for i, optionString := range contractSetupOptions {
		if s == optionString {
			option = deploymentOption(i)
			return
		}
	}

	err = errors.New(fmt.Sprintf("Invalid value for config option 'contractsetup'. The value is '%s', but must be one of '%v'.", s, contractSetupOptions))
	return
}

func parseAssetType(s string) (option assetType, err error) {
	s = strings.ToLower(s)
	for i, optionString := range assetTypeOptions {
		if s == optionString {
			option = assetType(i)
			return
		}
	}

	err = errors.New(fmt.Sprintf("Invalid value for config option 'asset.type'. The value is '%s', but must be one of '%v'.", s, assetTypeOptions))
	return
}

func parseHubSide(s string) (side hubSide, err error) {
	s = strings.ToLower(s)
	for i, optionString := range hubSides {
		if s == optionString {
			side = hubSide(i)
			return
		}
	}

	err = errors.New(fmt.Sprintf("Invalid value for config option 'hub.side'. The value is '%s', but must be one of '%v'.", s, hubSides))
	return
}
