// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"net"
	"strconv"

	"github.com/ethereum/go-ethereum/params"
	"github.com/pkg/errors"
	"perun.network/go-perun/wallet"
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

// strToAddress parses a string as wallet.Address
func strToAddress(str string) (wallet.Address, error) {
	if len(str) != 42 {
		return nil, errors.Errorf("Public keys must be chars 40 hex strings was '%s'", str)
	}
	h, err := hex.DecodeString(str[2:])
	if err != nil {
		return nil, errors.New("Could not parse address as hexadecimal")
	}
	addr, err := wallet.DecodeAddress(bytes.NewBuffer(h))
	return addr, errors.WithMessage(err, "string to address")
}

// etherToWei converts amount in "ether" (represented as float) to "wei" (represented as integer).
// It can provide exact results for values in the range of 1e-18 to 1e9.
func etherToWei(ethers ...*big.Float) []*big.Int {
	weis := make([]*big.Int, len(ethers))
	for idx, ether := range ethers {
		weiFloat := new(big.Float).Mul(ether, new(big.Float).SetFloat64(params.Ether))
		// accuracy (second return value) returns "exact" for specified input range, hence ignored.
		weis[idx], _ = weiFloat.Int(nil)
	}
	return weis
}

// weiToEther converts amount in "wei" (represented as integer) to "ether" (represented as float).
func weiToEther(weis ...*big.Int) []*big.Float {
	ethers := make([]*big.Float, len(weis))
	for idx, wei := range weis {
		ethers[idx] = new(big.Float).Quo(new(big.Float).SetInt(wei), new(big.Float).SetFloat64(params.Ether))
	}
	return ethers
}
