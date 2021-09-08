// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"math/big"
	"net"
	"strconv"

	dotwallet "github.com/perun-network/perun-polkadot-backend/wallet/sr25519"
	"github.com/pkg/errors"
)

func valBal(input string) error {
	_, _, err := big.ParseFloat(input, 10, 64, big.ToNearestEven)
	return errors.Wrap(err, "parsing float")
}

func valString(input string) error {
	if len(input) < 1 {
		return errors.New("Empty string")
	}
	return nil
}

func valID(input string) error {
	if _, err := strToAddress(input); err != nil {
		return errors.New("Invalid perun-id, must be an Ethereum address")
	}
	return nil
}

func valIP(input string) error {
	if val := net.ParseIP(input); val == nil {
		return errors.New("Invalid IP")
	}
	return nil
}

func valUInt(input string) error {
	if n, err := strconv.Atoi(input); err != nil {
		return errors.New("Invalid integer")
	} else if n < 0 {
		return errors.New("Value must be > 0")
	}
	return nil
}

func valPeer(arg string) error {
	if !backend.ExistsPeer(arg) {
		return errors.Errorf("Unknown peer, use 'info' to see connected")
	}
	return nil
}

func valAlias(arg string) error {
	for alias := range config.Peers {
		if alias == arg {
			return nil
		}
	}
	return errors.Errorf("Unknown alias, use 'config' to see available")
}

// strToAddress parses a string as dotwallet.Address
func strToAddress(str string) (*dotwallet.Address, error) {
	pk, err := dotwallet.NewPkFromHex(str)
	return dotwallet.NewAddressFromPk(pk), err
}

func dotToPlank(ethers ...*big.Float) []*big.Int {
	planks := make([]*big.Int, len(ethers))
	for idx, ether := range ethers {
		plankFloat := new(big.Float).Mul(ether, new(big.Float).SetFloat64(1e10))
		// accuracy (second return value) returns "exact" for specified input range, hence ignored.
		planks[idx], _ = plankFloat.Int(nil)
	}
	return planks
}

// plankToEther converts amount in "plank" (represented as integer) to "ether" (represented as float).
func plankToEther(planks ...*big.Int) []*big.Float {
	ethers := make([]*big.Float, len(planks))
	for idx, plank := range planks {
		ethers[idx] = new(big.Float).Quo(new(big.Float).SetInt(plank), new(big.Float).SetFloat64(1e10))
	}
	return ethers
}
