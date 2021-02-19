// Copyright (c) 2021 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo_test

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	any     = regexp.MustCompile(".+")
	timeout = time.Second * 30
)

type Cmd struct {
	*exec.Cmd
	stdin io.WriteCloser
}

func nodeCmd(name string) (*Cmd, error) {
	args := []string{
		"run", "../../main.go",
		"demo",
		"--config", fmt.Sprintf("../../%v.yaml", name),
		"--network", "../../network.yaml",
		"--log-level", "trace",
		"--log-file", fmt.Sprintf("%v.log", name),
		"--stdio",
	}
	cmd := exec.Command("go", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	return &Cmd{cmd, stdin}, nil
}

const (
	blockTime    = 5 * time.Second
	numUpdates   = 25
	ethUrl       = "ws://127.0.0.1:8545"
	addressAlice = "0x2EE1ac154435f542ECEc55C5b0367650d8A5343B"
	addressBob   = "0x70765701b79a4e973dAbb4b30A72f5a845f22F9E"
)

func TestNodes(t *testing.T) {
	// Start Alice.
	alice, err := nodeCmd("alice")
	require.NoError(t, err)
	require.NoError(t, alice.Start())
	defer alice.Process.Kill()
	time.Sleep(blockTime * 2) // Wait 2 blocks for contract deployment.

	// Start Bob.
	bob, err := nodeCmd("bob")
	require.NoError(t, err)
	require.NoError(t, bob.Start())
	defer bob.Process.Kill()
	time.Sleep(5 * time.Second) // Give Bob some time to initialize.

	// Get the initial on-chain balances from Alice and Bob.
	initBals, err := getOnChainBals()
	require.NoError(t, err)
	t.Logf("Initial on-chain balances: Alice = %f, Bob = %f", initBals[0], initBals[1])

	// Alice opens channel with Bob.
	require.NoError(t, alice.sendCommand("open bob 100 100\n"), "proposing channel")
	time.Sleep(3 * time.Second) // Ensure that Bob really received the proposal.
	require.NoError(t, bob.sendCommand("y\n"), "accepting channel proposal")
	t.Log("Opening channel…")
	time.Sleep(blockTime) // Wait 1 block for funding transactions to be confirmed.

	// Alice sends to Bob and Bob to Alice.
	for i := 0; i < numUpdates; i++ {
		t.Log("Sending payment… (alice->bob)")
		require.NoError(t, alice.sendCommand("send bob 1\n"))
		time.Sleep(100 * time.Millisecond)
		t.Log("Sending payment… (bob->alice)")
		require.NoError(t, bob.sendCommand("send alice 2\n"))
		time.Sleep(100 * time.Millisecond)
	}

	t.Log("Closing channel…")
	require.NoError(t, alice.sendCommand("close bob\n"))
	// Wait 2 blocks for the settle and withdrawal transactions plus some additional seconds.
	time.Sleep(2*blockTime + 5*time.Second)

	// Get the final balances from Alice and Bob after the settlement.
	finalBals, err := getOnChainBals()
	require.NoError(t, err)

	t.Logf("Final on-chain balances: Alice = %f, Bob = %f", finalBals[0], finalBals[1])

	var diffBals [2]float64

	// Calculate the differences between the initial and final balances.
	diffBals[0], _ = finalBals[0].Sub(finalBals[0], initBals[0]).Float64()
	diffBals[1], _ = finalBals[1].Sub(finalBals[1], initBals[1]).Float64()

	// Check the on-chain balance differences while allowing a higher deviation for Alice.
	assert.InEpsilon(t, numUpdates, diffBals[0], 0.1)
	assert.InEpsilon(t, -numUpdates, diffBals[1], 0.01)

	t.Log("Done")
}

func (cmd *Cmd) sendCommand(str string) error {
	_, err := io.WriteString(cmd.stdin, str)
	return err
}

func getOnChainBals() ([2]*big.Float, error) {
	var bals [2]*big.Float

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	ethereumBackend, err := ethclient.Dial(ethUrl)
	if err != nil {
		return bals, errors.New("Could not connect to ethereum node.")
	}

	for idx, adr := range [2]string{addressAlice, addressBob} {
		wei, err := ethereumBackend.BalanceAt(ctx,
			common.HexToAddress(adr), nil)
		if err != nil {
			return bals, err
		}
		bals[idx] = new(big.Float).Quo(new(big.Float).SetInt(wei),
			new(big.Float).SetFloat64(params.Ether))
	}

	return bals, nil
}
