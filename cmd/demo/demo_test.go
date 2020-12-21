// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo_test

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"testing"
	"time"

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

const blockTime = 5 * time.Second

func TestNodes(t *testing.T) {
	// Start Alice
	alice, err := nodeCmd("alice")
	require.NoError(t, err)
	require.NoError(t, alice.Start())
	defer alice.Process.Kill()
	time.Sleep(blockTime * time.Duration(2)) // wait 2 blocks for contract deployment

	// Start Bob
	bob, err := nodeCmd("bob")
	require.NoError(t, err)
	require.NoError(t, bob.Start())
	defer bob.Process.Kill()
	time.Sleep(5 * time.Second) // give bob some time to initialize

	// Alice opens channel with Bob
	require.NoError(t, alice.sendCommand("open bob 1000 1000\n"), "proposing channel")
	require.NoError(t, bob.sendCommand("y\n"), "accepting channel proposal")
	t.Log("Opening channel…")
	time.Sleep(blockTime) // wait 1 block for funding transactions to be confirmed

	// Alice sends to Bob and Bob to Alice
	for i := 0; i < 25; i++ {
		t.Log("Sending payment… (alice->bob)")
		require.NoError(t, alice.sendCommand("send bob 1\n"))
		time.Sleep(100 * time.Millisecond)
		t.Log("Sending payment… (bob->alice)")
		require.NoError(t, bob.sendCommand("send alice 2\n"))
		time.Sleep(100 * time.Millisecond)
	}
	t.Log("Done")
}

func (cmd *Cmd) sendCommand(str string) error {
	_, err := io.WriteString(cmd.stdin, str)
	return err
}
