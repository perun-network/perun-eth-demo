// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/pkg/errors"
	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
)

type (
	paymentChannel struct {
		*client.Channel

		log     log.Logger
		handler chan bool
		res     chan handlerRes
		onFinal func()
		// save the last state to circumvent the `channel.StateMtxd` problem
		lastState *channel.State
	}

	// A handlerRes encapsulates the result of a channel handling request
	handlerRes struct {
		up  client.ChannelUpdate
		err error
	}
)

func newPaymentChannel(ch *client.Channel, onFinal func()) *paymentChannel {
	go func() {
		l := log.WithField("channel", ch.ID())
		l.Debug("Watcher started")
		err := ch.Watch()
		l.WithError(err).Debug("Watcher stopped")
	}()
	return &paymentChannel{
		Channel:   ch,
		log:       log.WithField("channel", ch.ID()),
		handler:   make(chan bool, 1),
		res:       make(chan handlerRes),
		onFinal:   onFinal,
		lastState: ch.State(),
	}
}
func (ch *paymentChannel) sendMoney(amount *big.Int) error {
	return ch.sendUpdate(
		func(state *channel.State) {
			transferBal(stateBals(state), ch.Idx(), amount)
		}, "sendMoney")
}

func (ch *paymentChannel) sendFinal() error {
	ch.log.Debugf("Sending final state")
	return ch.sendUpdate(func(state *channel.State) {
		state.IsFinal = true
	}, "final")
}

func (ch *paymentChannel) sendUpdate(update func(*channel.State), desc string) error {
	ch.log.Debugf("Sending update: %s", desc)
	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.Timeout)
	defer cancel()

	state := ch.State().Clone()
	update(state)
	state.Version++
	balChanged := state.Balances[0][0].Cmp(ch.State().Balances[0][0]) != 0

	fmt.Printf("💭 Proposing update and waiting for confirmation...\n")

	err := ch.Update(ctx, client.ChannelUpdate{
		State:    state,
		ActorIdx: ch.Idx(),
	})
	ch.log.Debugf("Sent update: %s, err: %v", desc, err)

	if balChanged {
		bals := weiToEther(state.Allocation.Balances[0]...)
		fmt.Printf("💰 Sent payment. New balance: [My: %v Ξ, Peer: %v Ξ]\n", bals[ch.Idx()], bals[1-ch.Idx()]) // assumes two-party channel
	}
	if err == nil {
		ch.lastState = state
	}

	return err
}

func transferBal(bals []channel.Bal, ourIdx channel.Index, amount *big.Int) {
	a := new(big.Int).Set(amount) // local copy because we mutate it
	otherIdx := ourIdx ^ 1
	ourBal := bals[ourIdx]
	otherBal := bals[otherIdx]
	otherBal.Add(otherBal, a)
	ourBal.Sub(ourBal, a)
}

func stateBals(state *channel.State) []channel.Bal {
	return state.Balances[0]
}

func channelStateToString(state *channel.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ChannelID = %x\n", state.ID)
	fmt.Fprintf(&b, "Version = %v\n", state.Version)
	fmt.Fprintf(&b, "App = %v\n", state.App)
	fmt.Fprintf(&b, "Assets = [")
	for _, asset := range state.Assets {
		var buf bytes.Buffer
		asset.Encode(&buf)
		fmt.Fprintf(&b, "%x, ", buf.Bytes())
	}
	fmt.Fprintf(&b, "]\n")
	fmt.Fprintf(&b, "Balances = %v\n", state.Balances)
	fmt.Fprintf(&b, "Data = %v\n", state.Data)

	return b.String()
}

func (ch *paymentChannel) Handle(update client.ChannelUpdate, res *client.UpdateResponder) {
	oldBal := stateBals(ch.lastState)
	balChanged := oldBal[0].Cmp(update.State.Balances[0][0]) != 0

	actorAddress := ch.Peers()[0] // assuming two party channel
	alias, _ := findConfig(actorAddress)
	if alias == "" {
		alias = fmt.Sprintf("%v", actorAddress)
	}

	// TODO: implement print balance with support for arbitrary number of participants
	fmt.Printf("\n💭 New channel state proposed by %v:\n", alias)

	fmt.Println(channelStateToString(update.State))

	fmt.Printf("❓ Enter \"accept\" to accept, \"reject\" to reject:\n")

	// TODO: use prompt for input once available in package
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		ch.log.Error("reading user input")
		return
	}
	userInput := scanner.Text()

	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.Timeout)
	defer cancel()

	if userInput == "accept" {

		fmt.Println("✅ Channel update accepted")

		if err := res.Accept(ctx); err != nil {
			ch.log.Error(errors.WithMessage(err, "handling payment update"))
		}

		if balChanged {
			bals := weiToEther(update.State.Allocation.Balances[0]...)
			fmt.Printf("\n💰 Received payment. New balance: [My: %v Ξ, Peer: %v Ξ]\n", bals[ch.Idx()], bals[1-ch.Idx()])
		}
		if update.State.IsFinal {
			ch.log.Trace("Calling onFinal handler for paymentChannel")
			go ch.onFinal()
		}
		ch.lastState = update.State.Clone()

	} else {
		if err := res.Reject(ctx, "update rejected by user"); err != nil {
			ch.log.Error(errors.WithMessage(err, "rejecting update proposal"))
		}
		fmt.Println("❌ Channel update rejected")
	}
}

func (ch *paymentChannel) GetBalances() (our, other *big.Int) {
	bals := stateBals(ch.State())
	if len(bals) != 2 {
		return new(big.Int), new(big.Int)
	}
	return bals[ch.Idx()], bals[1-ch.Idx()]
}
