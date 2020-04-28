// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// +build on_chain_eth_test

package cmd_test

import (
	"fmt"
	"net"
	"regexp"
	"testing"
	"time"

	expect "github.com/google/goexpect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	any     = regexp.MustCompile(".+")
	timeout = time.Second * 30
)

func TestNodes(t *testing.T) {
	alice, _, err := expect.Spawn("go run ../main.go demo --config ../alice.yaml --network ../network.yaml --log-level trace --test-api true --log-file alice.log", -1)
	require.NoError(t, err)
	defer alice.Close()
	time.Sleep(time.Second * 2)

	bob, _, err := expect.Spawn("go run ../main.go demo --config ../bob.yaml --network ../network.yaml --log-level trace --log-file bob.log", -1)
	require.NoError(t, err)
	defer bob.Close()

	// Alice start
	_, _, e := alice.Expect(any, timeout)
	require.NoError(t, e)

	// Bob start
	_, _, e = bob.Expect(any, timeout)
	require.NoError(t, e)
	time.Sleep(time.Second * 5)

	// Alice connect to Bob
	require.NoError(t, sendSynchron(t, alice, "connect bob\n"), "connecting")
	t.Log("Alice connected")
	time.Sleep(time.Second * 1)
	// Alice open channel to Bob
	require.NoError(t, sendSynchron(t, alice, "open bob 1000 1000\n"), "opening")
	t.Log("Opening channel…")
	time.Sleep(time.Second * 5)
	// Alice send to Bob and Bob to Alice
	for i := 0; i < 25; i++ {
		t.Log("Sending payment… (alice->bob)")
		require.NoError(t, sendSynchron(t, alice, "send bob 1\n"))
		t.Log("Sending payment… (bob->alice)")
		require.NoError(t, sendSynchron(t, bob, "send alice 2\n"))
	}
	t.Log("Done")

	// Alice get balances
	time.Sleep(time.Second)
	b, err := getBalances()
	require.NoError(t, err)
	t.Log("Balances: ", b)
	assert.Equal(t, "{\"bob\":{\"My\":1025,\"Other\":975}}", b)
}

func getBalances() (string, error) {
	conn, err := net.Dial("tcp", "127.0.0.1:8080")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	fmt.Fprintf(conn, "getbals")
	buff := make([]byte, 1024)
	n, err := conn.Read(buff)
	if err != nil {
		return "", err
	}
	return string(buff[0:n]), nil
}

func sendSynchron(t *testing.T, obj *expect.GExpect, str string) error {
	for _, b := range []byte(str) {
		time.Sleep(time.Millisecond * 10)
		if err := obj.Send(string([]byte{b})); err != nil {
			return err
		}
	}
	_, _, err := obj.Expect(any, timeout)
	return err
}
